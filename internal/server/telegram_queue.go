package server

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"vibemonitor/internal/store"
)

const telegramQueueCapacity = 128
const telegramWorkers = 4

type telegramJob struct {
	id     string
	alerts *telegramAlerts
	config store.Config
}

// A node has at most one queued/in-flight check, preserving offline/recovery
// ordering. Different nodes cannot monopolize each other's HTTP worker.
func (s *Server) runTelegramAlerts(ctx context.Context) {
	client := telegramHTTPClient()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	s.runTelegramQueue(ctx, ticker.C, func(ctx context.Context, cfg store.Config, text string) error {
		return sendTelegramMessage(ctx, client, cfg, text)
	})
}

func (s *Server) runTelegramQueue(ctx context.Context, ticks <-chan time.Time, send telegramSender) {
	jobs := make(chan telegramJob, telegramQueueCapacity)
	done := make(chan string, telegramQueueCapacity+telegramWorkers)
	var workers sync.WaitGroup
	for i := 0; i < telegramWorkers; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case job := <-jobs:
					if ctx.Err() != nil {
						return
					}
					cfg := s.store.GetConfig()
					node := s.store.GetNode(job.id)
					if node != nil && telegramAlertEpoch(cfg) == telegramAlertEpoch(job.config) {
						job.alerts.check(ctx, cfg, []*store.Node{node})
					}
					select {
					case done <- job.id:
					case <-ctx.Done():
						return
					}
				}
			}
		}()
	}
	defer workers.Wait()
	engines := make(map[string]*telegramAlerts)
	pending := make(map[string]bool)
	cursor := 0
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-done:
			delete(pending, id)
		case <-ticks:
			cfg := s.store.GetConfig()
			nodes := s.store.GetAlertNodes()
			sort.Slice(nodes, func(i, j int) bool { return nodes[i].UUID < nodes[j].UUID })
			active := make(map[string]bool, len(nodes))
			for _, n := range nodes {
				if cfg.TelegramEnabled && (n.Profile == nil || !n.Profile.AlertsDisabled) {
					active[n.UUID] = true
				}
			}
			for id := range engines {
				if !active[id] && !pending[id] {
					delete(engines, id)
					_ = s.store.DeleteTelegramAlertState(id)
				}
			}
			if !cfg.TelegramEnabled || len(nodes) == 0 {
				continue
			}
			// Rotate admission when the queue is full so later nodes are not starved.
			for offset := 0; offset < len(nodes); offset++ {
				node := nodes[(cursor+offset)%len(nodes)]
				id := node.UUID
				if !active[id] || pending[id] {
					continue
				}
				engine := engines[id]
				if engine == nil {
					engine = newTelegramAlerts(func(sendCtx context.Context, captured store.Config, text string) error {
						current := s.store.GetConfig()
						if !current.TelegramEnabled || telegramAlertEpoch(current) != telegramAlertEpoch(captured) {
							return errors.New("alert configuration changed")
						}
						// Recheck node existence and opt-out immediately before sending.
						live := s.store.GetNode(id)
						if live == nil || (live.Profile != nil && live.Profile.AlertsDisabled) {
							return errors.New("node removed or alerts disabled")
						}
						return send(sendCtx, captured, text)
					})
					engine.load = func(epoch string) (map[string]store.TelegramAlertState, error) {
						return s.store.LoadTelegramAlertState(epoch, id)
					}
					engine.save = s.store.SaveTelegramAlertState
					engine.remove = s.store.DeleteTelegramAlertState
					engines[id] = engine
				}
				select {
				case jobs <- telegramJob{id, engine, cfg}:
					pending[id] = true
				default:
				}
			}
			cursor = (cursor + telegramQueueCapacity) % len(nodes)
		}
	}
}
