package store

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"vibemonitor/pkg/protocol"
)

const (
	MaxPingTargets          = 64
	MaxHistoryPoints        = 60
	OfflineThreshold        = 10 * time.Second
	MaxPingSamplesPerTarget = 1440 // 24 hours at 60-second intervals
	PingSampleIntervalSec   = 60   // Sample every 60 seconds
)

type PingSample struct {
	Host      string `json:"host"`
	Method    string `json:"method"`
	Timestamp int64  `json:"t"` // Unix timestamp in seconds
	Latency   int    `json:"l"` // ms, -1 for timeout
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
	Method  string       `json:"method,omitempty"`
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
	Profile     *NodeProfile        `json:"profile,omitempty"`
	PingPreview []PingPreview       `json:"ping_preview,omitempty"`
	UUID        string              `json:"uuid"`
	Token       string              `json:"token,omitempty"`
	Name        string              `json:"name"`
	Group       string              `json:"group"`
	Region      string              `json:"region"`
	Weight      int                 `json:"weight"`
	CreatedAt   time.Time           `json:"created_at"`
	LastSeen    time.Time           `json:"last_seen"`
	Online      bool                `json:"online"`
	ClientIP    string              `json:"client_ip,omitempty"`
	BasicInfo   *protocol.BasicInfo `json:"basic_info,omitempty"`
	LastReport  *protocol.Report    `json:"last_report,omitempty"`
	History     []HistoryPoint      `json:"history,omitempty"`

	// 60-second Ping Latency History (TargetName -> []PingSample, persisted in JSON)
	PingHistory map[string][]PingSample `json:"ping_history,omitempty"`

	// Traffic Billing Quota (sum mode: up + down)
	TrafficLimit       int64     `json:"traffic_limit"`      // Bytes, 0 = no limit
	ResetDay           int       `json:"reset_day"`          // 1-31 (day of month)
	InitialUsed        int64     `json:"initial_used"`       // Bytes, manual offset for current cycle
	CurrentCycleUsed   int64     `json:"current_cycle_used"` // Bytes, accumulated by agent in current cycle
	CycleStart         time.Time `json:"cycle_start"`        // Start timestamp of current cycle
	LastTotalUp        int64     `json:"last_total_up"`      // Last reported raw totalUp
	TrafficBaselineSet bool      `json:"traffic_baseline_set"`
	LastTotalDown      int64     `json:"last_total_down"` // Last reported raw totalDown

	// Computed dynamic fields
	CycleTotalUsed int64   `json:"cycle_total_used"` // InitialUsed + CurrentCycleUsed
	CycleRemaining int64   `json:"cycle_remaining"`  // TrafficLimit - CycleTotalUsed
	CyclePercent   float64 `json:"cycle_percent"`    // Percentage used (0-100+)
	DaysUntilReset int     `json:"days_until_reset"` // Days remaining until reset day
}

type Config struct {
	AdminUsername    string                `json:"admin_username"`
	AdminPassword    string                `json:"admin_password"`
	SiteTitle        string                `json:"site_title"`
	Announcement     string                `json:"announcement"`
	SiteIcon         string                `json:"site_icon,omitempty"`
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
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return hex.EncodeToString(b)
}

func GenerateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func New(filePath string, defaultAdminPassword string, usernames ...string) (*Store, error) {
	s := &Store{
		filePath:   filePath,
		nodes:      make(map[string]*Node),
		tokenIndex: make(map[string]string),
		stopFlush:  make(chan struct{}),
	}

	username := "admin"
	if len(usernames) > 0 && usernames[0] != "" {
		username = usernames[0]
	}
	if err := s.load(defaultAdminPassword, username); err != nil {
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
				if err := s.saveLocked(); err != nil {
					log.Printf("[Store] Save failed (will retry): %v", err)
				}
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

func (s *Store) load(defaultPassword, username string) error {
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
				AdminUsername:    username,
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

	if err := validatePingTargets(df.Config.PingTargets); err != nil {
		return fmt.Errorf("invalid saved ping targets: %w", err)
	}
	s.config = df.Config
	if s.config.AdminUsername == "" {
		s.config.AdminUsername = "admin"
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
		if n == nil {
			return errors.New("invalid null node in data file")
		}
		if n.Profile == nil {
			targets := append([]protocol.PingTarget{}, s.config.PingTargets...)
			for i := range targets {
				if !strings.Contains(targets[i].Host, ":") {
					targets[i].Host += ":80"
				}
			}
			n.Profile = &NodeProfile{Targets: targets}
		}
		if err := validateProfile(n.Profile); err != nil {
			return err
		}
		if !n.TrafficBaselineSet && n.ResetDay > 0 && n.LastReport != nil {
			n.TrafficBaselineSet = true
		}
		if n.Token != "" {
			s.tokenIndex[n.Token] = uuid
		}
	}
	if err := s.loadPingFile(data); err != nil {
		return err
	}
	s.prunePingDataLocked()
	s.dirty = true // Persist migration of legacy or unconfigured history.

	return nil
}

func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.filePath)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	df := DataFile{
		Config: s.config,
		Nodes:  s.nodesWithoutPing(),
	}
	data, err := json.MarshalIndent(df, "", "  ")
	if err != nil {
		return err
	}

	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0600); err != nil {
		return err
	}
	if err := s.savePingFile(data); err != nil {
		return err
	}
	if err := os.Rename(tmpFile, s.filePath); err != nil {
		return err
	}
	s.dirty = false
	return nil
}

func (s *Store) VerifyAdminPassword(pwd string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifyAdminPasswordLocked(pwd)
}

func hashAdminPassword(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(hashed), err
}

// verifyAdminPasswordLocked also migrates legacy plaintext passwords on the
// first successful login.
func (s *Store) verifyAdminPasswordLocked(pwd string) bool {
	if pwd == "" {
		return false
	}
	if bcrypt.CompareHashAndPassword([]byte(s.config.AdminPassword), []byte(pwd)) == nil {
		return true
	}
	// Backward compatibility for data files created before password hashing.
	if subtle.ConstantTimeCompare([]byte(pwd), []byte(s.config.AdminPassword)) != 1 {
		return false
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(pwd), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("[Store] Password migration failed: %v", err)
		return true
	}
	previous := s.config.AdminPassword
	s.config.AdminPassword = string(hashed)
	if err := s.saveLocked(); err != nil {
		s.config.AdminPassword = previous
		log.Printf("[Store] Password migration save failed: %v", err)
	}
	return true
}

func (s *Store) SetAdminPassword(newPwd string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if newPwd == "" {
		return errors.New("admin password cannot be empty")
	}
	hashed, err := hashAdminPassword(newPwd)
	if err != nil {
		return err
	}
	previous := s.config
	s.config.AdminPassword = hashed
	if err := s.saveLocked(); err != nil {
		s.config = previous
		return err
	}
	return nil
}

func (s *Store) GetConfig() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config
}

func (s *Store) UpdateConfig(title, announcement, autoKey string, pingTargets []protocol.PingTarget) error {
	if err := validatePingTargets(pingTargets); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.config
	if title != "" {
		next.SiteTitle = title
	}
	next.Announcement = announcement
	if autoKey != "" {
		next.AutoDiscoveryKey = autoKey
	}
	if pingTargets != nil {
		next.PingTargets = pingTargets
	}
	return s.commitConfigLocked(next)
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

	if day >= clampDay(year, month, resetDay) {
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
	threshold := OfflineThreshold
	var interval float64
	if n.BasicInfo != nil {
		interval = n.BasicInfo.ReportIntervalSeconds
	}
	if n.LastReport != nil && n.LastReport.ReportIntervalSeconds > 0 {
		interval = n.LastReport.ReportIntervalSeconds
	}
	if interval > 0 && interval <= 3600 {
		if adaptive := time.Duration(interval * 3 * float64(time.Second)); adaptive > threshold {
			threshold = adaptive
		}
	}
	n.Online = !n.LastSeen.IsZero() && now.Sub(n.LastSeen) < threshold

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
		nodeCopy.Token = "" // Credentials are only available through authenticated management.
		nodeCopy.ClientIP = ""
		if n.BasicInfo != nil {
			basicInfo := *n.BasicInfo
			basicInfo.IPv4 = ""
			basicInfo.IPv6 = ""
			nodeCopy.BasicInfo = &basicInfo
		}
		if n.Profile != nil {
			profile := *n.Profile
			profile.Targets = append([]protocol.PingTarget(nil), profile.Targets...)
			for i := range profile.Targets {
				profile.Targets[i].Host = ""
			}
			nodeCopy.Profile = &profile
		}
		nodeCopy.PingHistory = nil // Kept compact for dashboard list
		nodeCopy.PingPreview = s.pingPreviewLocked(n)
		for i := range nodeCopy.PingPreview {
			nodeCopy.PingPreview[i].Host = ""
			for j := range nodeCopy.PingPreview[i].Samples {
				nodeCopy.PingPreview[i].Samples[j].Host = ""
			}
		}
		if nodeCopy.LastReport != nil {
			report := *nodeCopy.LastReport
			report.PingResults = append([]protocol.PingResult(nil), report.PingResults...)
			for i := range report.PingResults {
				report.PingResults[i].Host = ""
			}
			nodeCopy.LastReport = &report
		}
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
	copy := *s.nodes[uuid]
	return &copy
}

type NodeOptions struct {
	Profile        *NodeProfile
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
	if err := validateProfile(opts.Profile); err != nil {
		return nil, err
	}
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
		Profile:          opts.Profile,
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
		delete(s.nodes, uuid)
		delete(s.tokenIndex, token)
		return nil, err
	}
	s.notifyUpdate()
	copy := *node
	return &copy, nil
}

func (s *Store) UpdateNode(uuid, name, group, region string, weight int) error {
	return s.UpdateNodeWithOptions(uuid, NodeOptions{
		Name:           name,
		Group:          group,
		Region:         region,
		Weight:         weight,
		ResetDay:       -1,
		TrafficLimitGB: -1,
		InitialUsedGB:  -1,
	})
}

func (s *Store) UpdateNodeWithOptions(uuid string, opts NodeOptions) error {
	if err := validateProfile(opts.Profile); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.nodes[uuid]
	if !ok {
		return errors.New("node not found")
	}
	previous := *n
	if opts.Profile != nil {
		n.Profile = opts.Profile
		s.pruneNodePingLocked(n)
	}
	n.checkCycleRollover(time.Now())
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
		*n = previous
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
		s.nodes[uuid] = n
		s.tokenIndex[n.Token] = uuid
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
		return nil, errors.New("unauthorized client token")
	}

	node := s.nodes[uuid]
	node.BasicInfo = &info
	s.dirty = true
	if clientIP != "" {
		node.ClientIP = clientIP
	}
	node.LastSeen = time.Now().UTC()
	node.Online = true

	if err := s.saveLocked(); err != nil {
		log.Printf("[Store] Save failed (will retry): %v", err)
	}
	s.notifyUpdate()
	copy := *node
	return &copy, nil
}

func (s *Store) IngestReport(tokenOrUUID string, report protocol.Report, clientIP string) (*Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	uuid, ok := s.tokenIndex[tokenOrUUID]
	if !ok {
		return nil, errors.New("unauthorized client token")
	}

	if len(report.PingResults) > MaxPingTargets {
		return nil, errors.New("too many ping results")
	}
	report.PingResults = filterPingResults(report.PingResults, s.targetsLocked(s.nodes[uuid]))
	if report.Network.TotalUp < 0 || report.Network.TotalDown < 0 {
		return nil, errors.New("negative network counters")
	}
	node := s.nodes[uuid]
	node.LastReport = &report
	s.dirty = true
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

		if node.TrafficBaselineSet && curUp >= node.LastTotalUp {
			node.CurrentCycleUsed += (curUp - node.LastTotalUp)
		} else if node.TrafficBaselineSet && curUp < node.LastTotalUp {
			// Reboot detected: counter reset to 0 and started afresh
			node.CurrentCycleUsed += curUp
		}

		if node.TrafficBaselineSet && curDown >= node.LastTotalDown {
			node.CurrentCycleUsed += (curDown - node.LastTotalDown)
		} else if node.TrafficBaselineSet && curDown < node.LastTotalDown {
			// Reboot detected
			node.CurrentCycleUsed += curDown
		}

		node.TrafficBaselineSet = true
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
					Host:      p.Host,
					Method:    p.Method,
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
			if err := s.saveLocked(); err != nil {
				log.Printf("[Store] Save failed (will retry): %v", err)
			}
		}
	}

	s.dirty = true
	s.notifyUpdate()
	copy := *node
	return &copy, nil
}

func (s *Store) GetPingHistory(uuid, targetName, timeRange string) (*PingHistoryResponse, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node, ok := s.nodes[uuid]
	if !ok {
		return nil, errors.New("node not found")
	}

	targets := s.targetsLocked(node)
	if targetName == "" && len(targets) > 0 {
		targetName = targets[0].Name
	}
	var targetHost string
	for _, target := range targets {
		if target.Name == targetName {
			targetHost = target.Host
			break
		}
	}
	allSamples := node.PingHistory[targetName]
	method := ""
	for i := len(allSamples) - 1; i >= 0; i-- {
		if allSamples[i].Host == targetHost && targetHost != "" {
			method = allSamples[i].Method
			break
		}
	}
	nowUnix := time.Now().Unix()
	var duration int64 = 86400 // default 24h
	if timeRange == "1h" {
		duration = 3600
	}

	cutoff := nowUnix - duration
	filtered := make([]PingSample, 0, len(allSamples))
	for _, smp := range allSamples {
		if smp.Timestamp >= cutoff && targetHost != "" && smp.Host == targetHost && smp.Method == method {
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

	return &PingHistoryResponse{
		Method:  method,
		UUID:    uuid,
		Target:  targetName,
		Host:    targetHost,
		Range:   timeRange,
		Stats:   stats,
		Samples: filtered,
	}, nil
}

func (s *Store) VerifyAdmin(username, password string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if username != s.config.AdminUsername || password == "" {
		return false
	}
	return s.verifyAdminPasswordLocked(password)
}
