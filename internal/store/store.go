package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"vibemonitor/pkg/protocol"
)

const (
	MaxHistoryPoints        = 60
	OfflineThreshold        = 10 * time.Second
	MaxPingSamplesPerTarget = 1440 // 24 hours at 60-second intervals
	PingSampleIntervalSec   = 60   // Sample every 60 seconds
)

type PingSample struct {
	Timestamp int64 `json:"t"` // Unix timestamp in seconds
	Latency   int   `json:"l"` // ms, -1 for timeout
}

type PingStats struct {
	Current    int     `json:"current"`
	Avg        float64 `json:"avg"`
	Min        int     `json:"min"`
	Max        int     `json:"max"`
	PacketLoss float64 `json:"packet_loss"` // Percentage, e.g. 0.0 - 100.0%
	TotalCount int     `json:"total_count"`
}

type PingHistoryResponse struct {
	UUID    string       `json:"uuid"`
	Target  string       `json:"target"`
	Host    string       `json:"host,omitempty"`
	Range   string       `json:"range"` // "1h" or "24h"
	Stats   PingStats    `json:"stats"`
	Samples []PingSample `json:"samples"`
}

type HistoryPoint struct {
	Timestamp int64   `json:"timestamp"`
	CPUUsage  float64 `json:"cpu_usage"`
	RAMUsage  float64 `json:"ram_usage"`
	NetUp     int64   `json:"net_up"`
	NetDown   int64   `json:"net_down"`
}

type Node struct {
	UUID        string               `json:"uuid"`
	Token       string               `json:"token"`
	Name        string               `json:"name"`
	Group       string               `json:"group"`
	Region      string               `json:"region"`
	Weight      int                  `json:"weight"`
	CreatedAt   time.Time            `json:"created_at"`
	LastSeen    time.Time            `json:"last_seen"`
	Online      bool                 `json:"online"`
	ClientIP    string               `json:"client_ip,omitempty"`
	BasicInfo   *protocol.BasicInfo  `json:"basic_info,omitempty"`
	LastReport  *protocol.Report     `json:"last_report,omitempty"`
	History     []HistoryPoint       `json:"history,omitempty"`

	// 60-second Ping Latency History (TargetName -> []PingSample, persisted in JSON)
	PingHistory map[string][]PingSample `json:"ping_history,omitempty"`

	// Traffic Billing Quota (sum mode: up + down)
	TrafficLimit     int64     `json:"traffic_limit"`      // Bytes, 0 = no limit
	ResetDay         int       `json:"reset_day"`          // 1-31 (day of month)
	InitialUsed      int64     `json:"initial_used"`       // Bytes, manual offset for current cycle
	CurrentCycleUsed int64     `json:"current_cycle_used"` // Bytes, accumulated by agent in current cycle
	CycleStart       time.Time `json:"cycle_start"`        // Start timestamp of current cycle
	LastTotalUp      int64     `json:"last_total_up"`      // Last reported raw totalUp
	LastTotalDown    int64     `json:"last_total_down"`    // Last reported raw totalDown

	// Computed dynamic fields
	CycleTotalUsed int64   `json:"cycle_total_used"` // InitialUsed + CurrentCycleUsed
	CycleRemaining int64   `json:"cycle_remaining"`  // TrafficLimit - CycleTotalUsed
	CyclePercent   float64 `json:"cycle_percent"`    // Percentage used (0-100+)
	DaysUntilReset int     `json:"days_until_reset"` // Days remaining until reset day
}

type Config struct {
	AdminPassword    string                `json:"admin_password"`
	SiteTitle        string                `json:"site_title"`
	Announcement     string                `json:"announcement"`
	AutoDiscoveryKey string                `json:"auto_discovery_key"`
	PingTargets      []protocol.PingTarget `json:"ping_targets"`
}

type DataFile struct {
	Config Config           `json:"config"`
	Nodes  map[string]*Node `json:"nodes"`
}

type Store struct {
	mu         sync.RWMutex
	filePath   string
	config     Config
	nodes      map[string]*Node  // uuid -> Node
	tokenIndex map[string]string // token -> uuid
	onUpdate   func()            // optional callback when state changes
	dirty      bool
	stopFlush  chan struct{}
}

func GenerateToken(length int) string {
	b := make([]byte, length)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func New(filePath string, defaultAdminPassword string) (*Store, error) {
	s := &Store{
		filePath:   filePath,
		nodes:      make(map[string]*Node),
		tokenIndex: make(map[string]string),
		stopFlush:  make(chan struct{}),
	}

	if err := s.load(defaultAdminPassword); err != nil {
		return nil, err
	}
	go s.periodicFlusher()
	return s, nil
}

func (s *Store) periodicFlusher() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-s.stopFlush:
			return
		case <-ticker.C:
			s.mu.Lock()
			if s.dirty {
				_ = s.saveLocked()
			}
			s.mu.Unlock()
		}
	}
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveLocked()
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	select {
	case <-s.stopFlush:
	default:
		close(s.stopFlush)
	}
	if s.dirty {
		return s.saveLocked()
	}
	return nil
}

func (s *Store) SetOnUpdate(fn func()) {
	s.mu.Lock()
	s.onUpdate = fn
	s.mu.Unlock()
}

func (s *Store) notifyUpdate() {
	if s.onUpdate != nil {
		go s.onUpdate()
	}
}

func (s *Store) load(defaultPassword string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// First run initialization
			if defaultPassword == "" {
				defaultPassword = GenerateToken(6) // 12-char random hex password
				log.Printf("=====================================================")
				log.Printf(" [INITIAL SETUP] Generated Admin Password: %s", defaultPassword)
				log.Printf(" Please save this password to login to the dashboard!")
				log.Printf("=====================================================")
			}
			s.config = Config{
				AdminPassword:    defaultPassword,
				SiteTitle:        "VibeMonitor",
				AutoDiscoveryKey: GenerateToken(16),
				PingTargets:      []protocol.PingTarget{},
			}
			return s.saveLocked()
		}
		return err
	}

	var df DataFile
	if err := json.Unmarshal(data, &df); err != nil {
		return fmt.Errorf("failed to parse data file %s: %w", s.filePath, err)
	}

	s.config = df.Config
	if defaultPassword != "" {
		s.config.AdminPassword = defaultPassword
	}
	if s.config.SiteTitle == "" {
		s.config.SiteTitle = "VibeMonitor"
	}
	if s.config.AutoDiscoveryKey == "" {
		s.config.AutoDiscoveryKey = GenerateToken(16)
	}
	if s.config.PingTargets == nil {
		s.config.PingTargets = []protocol.PingTarget{}
	}

	s.nodes = df.Nodes
	if s.nodes == nil {
		s.nodes = make(map[string]*Node)
	}
	for uuid, n := range s.nodes {
		if n.Token != "" {
			s.tokenIndex[n.Token] = uuid
		}
	}
	return nil
}

func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.filePath)
	if dir != "." && dir != "" {
		_ = os.MkdirAll(dir, 0755)
	}

	df := DataFile{
		Config: s.config,
		Nodes:  s.nodes,
	}
	data, err := json.MarshalIndent(df, "", "  ")
	if err != nil {
		return err
	}

	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return err
	}
	if err := os.Rename(tmpFile, s.filePath); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func (s *Store) VerifyAdminPassword(pwd string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return pwd != "" && s.config.AdminPassword == pwd
}

func (s *Store) SetAdminPassword(newPwd string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if newPwd == "" {
		return errors.New("admin password cannot be empty")
	}
	s.config.AdminPassword = newPwd
	return s.saveLocked()
}

func (s *Store) GetConfig() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *Store) UpdateConfig(title, announcement, autoKey string, pingTargets []protocol.PingTarget) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if title != "" {
		s.config.SiteTitle = title
	}
	s.config.Announcement = announcement
	if autoKey != "" {
		s.config.AutoDiscoveryKey = autoKey
	}
	if pingTargets != nil {
		s.config.PingTargets = pingTargets
	}
	if err := s.saveLocked(); err != nil {
		return err
	}
	s.notifyUpdate()
	return nil
}

// GetBillingCycleRange returns the start and end time of the billing cycle.
// For example, if resetDay is 15 and now is Sep 4, cycle is Aug 15 00:00 to Sep 15 00:00.
// If resetDay is 15 and now is Sep 20, cycle is Sep 15 00:00 to Oct 15 00:00.
func GetBillingCycleRange(resetDay int, now time.Time) (start, end time.Time) {
	if resetDay < 1 {
		resetDay = 1
	} else if resetDay > 31 {
		resetDay = 31
	}

	year, month, day := now.Date()

	clampDay := func(y int, m time.Month, targetDay int) int {
		daysInMonth := time.Date(y, m+1, 0, 0, 0, 0, 0, now.Location()).Day()
		if targetDay > daysInMonth {
			return daysInMonth
		}
		return targetDay
	}

	if day >= resetDay {
		startDay := clampDay(year, month, resetDay)
		start = time.Date(year, month, startDay, 0, 0, 0, 0, now.Location())

		nextYear, nextMonth := year, month+1
		if nextMonth > 12 {
			nextYear++
			nextMonth = 1
		}
		endDay := clampDay(nextYear, nextMonth, resetDay)
		end = time.Date(nextYear, nextMonth, endDay, 0, 0, 0, 0, now.Location())
	} else {
		prevYear, prevMonth := year, month-1
		if prevMonth < 1 {
			prevYear--
			prevMonth = 12
		}
		startDay := clampDay(prevYear, prevMonth, resetDay)
		start = time.Date(prevYear, prevMonth, startDay, 0, 0, 0, 0, now.Location())

		endDay := clampDay(year, month, resetDay)
		end = time.Date(year, month, endDay, 0, 0, 0, 0, now.Location())
	}
	return start, end
}

func (n *Node) checkCycleRollover(now time.Time) {
	if n.ResetDay <= 0 {
		return
	}
	start, _ := GetBillingCycleRange(n.ResetDay, now)
	if n.CycleStart.IsZero() || n.CycleStart.Before(start) {
		// New billing cycle arrived!
		n.CycleStart = start
		n.CurrentCycleUsed = 0
		n.InitialUsed = 0 // one-time manual offset is cleared on subsequent cycles
	}
}

func (n *Node) calculateDynamicFields(now time.Time) {
	n.Online = !n.LastSeen.IsZero() && now.Sub(n.LastSeen) < OfflineThreshold

	if n.ResetDay > 0 {
		n.checkCycleRollover(now)
		_, cycleEnd := GetBillingCycleRange(n.ResetDay, now)
		duration := cycleEnd.Sub(now)
		days := int(duration.Hours() / 24)
		if days < 0 {
			days = 0
		}
		n.DaysUntilReset = days
	}

	n.CycleTotalUsed = n.InitialUsed + n.CurrentCycleUsed
	if n.TrafficLimit > 0 {
		n.CycleRemaining = n.TrafficLimit - n.CycleTotalUsed
		n.CyclePercent = float64(n.CycleTotalUsed) / float64(n.TrafficLimit) * 100.0
	}
}

func (s *Store) GetNodes() []*Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now()
	list := make([]*Node, 0, len(s.nodes))
	for _, n := range s.nodes {
		nodeCopy := *n
		nodeCopy.PingHistory = nil // Kept compact for dashboard list
		nodeCopy.calculateDynamicFields(now)
		list = append(list, &nodeCopy)
	}
	return list
}

func (s *Store) GetNode(uuid string) *Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, ok := s.nodes[uuid]
	if !ok {
		return nil
	}
	nodeCopy := *n
	nodeCopy.calculateDynamicFields(time.Now())
	return &nodeCopy
}

func (s *Store) FindNodeByToken(token string) *Node {
	s.mu.RLock()
	defer s.mu.RUnlock()

	uuid, ok := s.tokenIndex[token]
	if !ok {
		return nil
	}
	return s.nodes[uuid]
}

type NodeOptions struct {
	Name           string
	Group          string
	Region         string
	Weight         int
	TrafficLimitGB float64
	ResetDay       int
	InitialUsedGB  float64
}

func (s *Store) CreateNode(name, group, region string) (*Node, error) {
	return s.CreateNodeWithOptions(NodeOptions{
		Name:   name,
		Group:  group,
		Region: region,
	})
}

func (s *Store) CreateNodeWithOptions(opts NodeOptions) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	uuid := GenerateUUID()
	token := GenerateToken(16)

	if opts.Name == "" {
		opts.Name = fmt.Sprintf("node-%s", uuid[:8])
	}
	if opts.Region == "" {
		opts.Region = "DEFAULT"
	}

	trafficLimitBytes := int64(opts.TrafficLimitGB * 1024 * 1024 * 1024)
	initialUsedBytes := int64(opts.InitialUsedGB * 1024 * 1024 * 1024)

	now := time.Now()
	var cycleStart time.Time
	if opts.ResetDay > 0 {
		cycleStart, _ = GetBillingCycleRange(opts.ResetDay, now)
	}

	node := &Node{
		UUID:             uuid,
		Token:            token,
		Name:             opts.Name,
		Group:            opts.Group,
		Region:           opts.Region,
		Weight:           opts.Weight,
		TrafficLimit:     trafficLimitBytes,
		ResetDay:         opts.ResetDay,
		InitialUsed:      initialUsedBytes,
		CurrentCycleUsed: 0,
		CycleStart:       cycleStart,
		CreatedAt:        time.Now().UTC(),
		History:          make([]HistoryPoint, 0, MaxHistoryPoints),
	}

	s.nodes[uuid] = node
	s.tokenIndex[token] = uuid

	if err := s.saveLocked(); err != nil {
		return nil, err
	}
	s.notifyUpdate()
	return node, nil
}

func (s *Store) UpdateNode(uuid, name, group, region string, weight int) error {
	return s.UpdateNodeWithOptions(uuid, NodeOptions{
		Name:   name,
		Group:  group,
		Region: region,
		Weight: weight,
		ResetDay: -1,
		TrafficLimitGB: -1,
		InitialUsedGB: -1,
	})
}

func (s *Store) UpdateNodeWithOptions(uuid string, opts NodeOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.nodes[uuid]
	if !ok {
		return errors.New("node not found")
	}
	if opts.Name != "" {
		n.Name = opts.Name
	}
	n.Group = opts.Group
	if opts.Region != "" {
		n.Region = opts.Region
	}
	n.Weight = opts.Weight

	if opts.TrafficLimitGB >= 0 {
		n.TrafficLimit = int64(opts.TrafficLimitGB * 1024 * 1024 * 1024)
	}
	if opts.ResetDay >= 0 && opts.ResetDay <= 31 {
		n.ResetDay = opts.ResetDay
		if n.ResetDay > 0 {
			n.CycleStart, _ = GetBillingCycleRange(n.ResetDay, time.Now())
		}
	}
	if opts.InitialUsedGB >= 0 {
		n.InitialUsed = int64(opts.InitialUsedGB * 1024 * 1024 * 1024)
	}

	if err := s.saveLocked(); err != nil {
		return err
	}
	s.notifyUpdate()
	return nil
}

func (s *Store) DeleteNode(uuid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.nodes[uuid]
	if !ok {
		return errors.New("node not found")
	}
	delete(s.tokenIndex, n.Token)
	delete(s.nodes, uuid)

	if err := s.saveLocked(); err != nil {
		return err
	}
	s.notifyUpdate()
	return nil
}

func (s *Store) IngestBasicInfo(tokenOrUUID string, info protocol.BasicInfo, clientIP string) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	uuid, ok := s.tokenIndex[tokenOrUUID]
	if !ok {
		// Try matching by UUID directly
		if _, exists := s.nodes[tokenOrUUID]; exists {
			uuid = tokenOrUUID
		} else {
			return nil, errors.New("unauthorized client token")
		}
	}

	node := s.nodes[uuid]
	node.BasicInfo = &info
	if clientIP != "" {
		node.ClientIP = clientIP
	}
	node.LastSeen = time.Now().UTC()
	node.Online = true

	_ = s.saveLocked()
	s.notifyUpdate()
	return node, nil
}

func (s *Store) IngestReport(tokenOrUUID string, report protocol.Report, clientIP string) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	uuid, ok := s.tokenIndex[tokenOrUUID]
	if !ok {
		if _, exists := s.nodes[tokenOrUUID]; exists {
			uuid = tokenOrUUID
		} else {
			return nil, errors.New("unauthorized client token")
		}
	}

	node := s.nodes[uuid]
	node.LastReport = &report
	node.LastSeen = time.Now().UTC()
	node.Online = true
	if clientIP != "" && node.ClientIP == "" {
		node.ClientIP = clientIP
	}

	// Traffic delta accounting for billing cycle
	if node.ResetDay > 0 {
		node.checkCycleRollover(time.Now())

		curUp := report.Network.TotalUp
		curDown := report.Network.TotalDown

		if node.LastTotalUp > 0 && curUp >= node.LastTotalUp {
			node.CurrentCycleUsed += (curUp - node.LastTotalUp)
		} else if curUp > 0 && curUp < node.LastTotalUp {
			// Reboot detected: counter reset to 0 and started afresh
			node.CurrentCycleUsed += curUp
		}

		if node.LastTotalDown > 0 && curDown >= node.LastTotalDown {
			node.CurrentCycleUsed += (curDown - node.LastTotalDown)
		} else if curDown > 0 && curDown < node.LastTotalDown {
			// Reboot detected
			node.CurrentCycleUsed += curDown
		}

		node.LastTotalUp = curUp
		node.LastTotalDown = curDown
	}

	// Append history point
	var ramUsagePct float64
	if report.RAM.Total > 0 {
		ramUsagePct = float64(report.RAM.Used) / float64(report.RAM.Total) * 100.0
	}
	hp := HistoryPoint{
		Timestamp: time.Now().Unix(),
		CPUUsage:  report.CPU.Usage,
		RAMUsage:  ramUsagePct,
		NetUp:     report.Network.Up,
		NetDown:   report.Network.Down,
	}

	if len(node.History) >= MaxHistoryPoints {
		node.History = append(node.History[1:], hp)
	} else {
		node.History = append(node.History, hp)
	}

	// Record 60-second interval Ping samples into PingHistory (persisted in JSON)
	if len(report.PingResults) > 0 {
		if node.PingHistory == nil {
			node.PingHistory = make(map[string][]PingSample)
		}
		nowUnix := time.Now().Unix()
		addedSample := false
		for _, p := range report.PingResults {
			samples := node.PingHistory[p.Name]
			if len(samples) == 0 || (nowUnix-samples[len(samples)-1].Timestamp) >= PingSampleIntervalSec {
				samples = append(samples, PingSample{
					Timestamp: nowUnix,
					Latency:   p.Latency,
				})
				if len(samples) > MaxPingSamplesPerTarget {
					samples = samples[len(samples)-MaxPingSamplesPerTarget:]
				}
				node.PingHistory[p.Name] = samples
				addedSample = true
			}
		}
		if addedSample {
			_ = s.saveLocked()
		}
	}

	s.dirty = true
	s.notifyUpdate()
	return node, nil
}

func (s *Store) GetPingHistory(uuid, targetName, timeRange string) (*PingHistoryResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node, ok := s.nodes[uuid]
	if !ok {
		return nil, errors.New("node not found")
	}

	if targetName == "" {
		for name := range node.PingHistory {
			targetName = name
			break
		}
		if targetName == "" && len(s.config.PingTargets) > 0 {
			targetName = s.config.PingTargets[0].Name
		}
	}

	allSamples := node.PingHistory[targetName]
	nowUnix := time.Now().Unix()
	var duration int64 = 86400 // default 24h
	if timeRange == "1h" {
		duration = 3600
	}

	cutoff := nowUnix - duration
	filtered := make([]PingSample, 0, len(allSamples))
	for _, smp := range allSamples {
		if smp.Timestamp >= cutoff {
			filtered = append(filtered, smp)
		}
	}

	stats := PingStats{
		Current:    -1,
		TotalCount: len(filtered),
	}

	if len(filtered) > 0 {
		stats.Current = filtered[len(filtered)-1].Latency
		validCount := 0
		lostCount := 0
		var sum int64 = 0
		minVal := 999999
		maxVal := -1

		for _, smp := range filtered {
			if smp.Latency < 0 {
				lostCount++
			} else {
				validCount++
				sum += int64(smp.Latency)
				if smp.Latency < minVal {
					minVal = smp.Latency
				}
				if smp.Latency > maxVal {
					maxVal = smp.Latency
				}
			}
		}

		if validCount > 0 {
			stats.Avg = math.Round(float64(sum)/float64(validCount)*10.0) / 10.0
			stats.Min = minVal
			stats.Max = maxVal
		} else {
			stats.Min = -1
			stats.Max = -1
		}
		stats.PacketLoss = math.Round(float64(lostCount)/float64(len(filtered))*1000.0) / 10.0
	}

	var targetHost string
	for _, pt := range s.config.PingTargets {
		if pt.Name == targetName {
			targetHost = pt.Host
			break
		}
	}

	return &PingHistoryResponse{
		UUID:    uuid,
		Target:  targetName,
		Host:    targetHost,
		Range:   timeRange,
		Stats:   stats,
		Samples: filtered,
	}, nil
}

