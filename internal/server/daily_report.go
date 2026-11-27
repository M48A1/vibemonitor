package server

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"vibemonitor/internal/store"
)

var dailyReportBeijing = time.FixedZone("UTC+8", 8*3600)

func (s *Server) runDailyReports(ctx context.Context) {
	client := telegramHTTPClient()
	send := func(ctx context.Context, cfg store.Config, message string) error {
		return sendTelegramMessage(ctx, client, cfg, message)
	}
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		if err := s.checkDailyReports(ctx, time.Now(), send); err != nil && ctx.Err() == nil {
			log.Printf("[Daily report] %v (will retry)", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) checkDailyReports(ctx context.Context, now time.Time, send telegramSender) error {
	if err := s.store.CollectDailyReportsContext(ctx, now); err != nil {
		return fmt.Errorf("collect daily summaries: %w", err)
	}
	cfg := s.store.GetConfig()
	local := now.In(dailyReportBeijing)
	if !cfg.TelegramDailyReportEnabled || local.Hour() < cfg.TelegramDailyReportHour ||
		(local.Hour() == 0 && local.Minute() < 5) {
		return nil
	}
	date := local.AddDate(0, 0, -1).Format(time.DateOnly)
	parts, sent, err := s.store.LoadDailyTelegramContext(ctx, date)
	if err != nil {
		return err
	}
	if len(parts) == 0 {
		dataDate, reports, bandwidth, err := s.store.BeijingDailyReportContext(ctx, now)
		if err != nil {
			return err
		}
		parts = dailyReportMessages(local.Format(time.DateOnly), dataDate, reports, s.store.GetAlertNodes(), bandwidth)
		if err := s.store.QueueDailyTelegramContext(ctx, date, parts); err != nil {
			return err
		}
		parts, sent, err = s.store.LoadDailyTelegramContext(ctx, date)
		if err != nil {
			return err
		}
	}
	for ; sent < len(parts); sent++ {
		current := s.store.GetConfig()
		if !current.TelegramDailyReportEnabled || current.TelegramBotToken != cfg.TelegramBotToken ||
			current.TelegramChatID != cfg.TelegramChatID {
			break
		}
		if err := send(ctx, cfg, parts[sent]); err != nil {
			return err
		}
		// Persist each acknowledged part so a retry can resume a split report.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := s.store.MarkDailyTelegramPartContext(saveCtx, date, sent+1)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func dailyReportMessages(date, dataDate string, reports []store.NodeDailyReport, nodes []*store.Node, bandwidth store.ReportBandwidth) []string {
	title := fmt.Sprintf("每日运行指数报告 (%s)", date)
	lines := []string{
		fmt.Sprintf("统计日期：%s（UTC+8）", dataDate),
		"", "1. 节点概览",
	}
	online, offlineSeconds, drops, unknown := 0, 0.0, 0, 0
	for _, n := range nodes {
		if n.Online {
			online++
		}
	}
	for _, n := range reports {
		drops += n.Drops
		offlineSeconds += n.OfflineSeconds
		if n.CoverageSeconds < float64(n.DaySeconds) {
			unknown++
		}
	}
	lines = append(lines, fmt.Sprintf("• 在线状态：%d / %d 在线（发送前快照）", online, len(nodes)))
	if len(reports) == 0 || unknown == len(reports) && drops == 0 && offlineSeconds == 0 {
		lines = append(lines, "• 昨日掉线：数据不足")
	} else {
		lines = append(lines, fmt.Sprintf("• 昨日掉线：%d 次，累计 %s", drops, reportDuration(offlineSeconds)))
	}
	if unknown > 0 {
		lines = append(lines, fmt.Sprintf("• %d 个节点的昨日在线记录覆盖不足", unknown))
	}
	for _, n := range reports {
		if n.OfflineSeconds > 0 {
			lines = append(lines, fmt.Sprintf("  %s：%d 次，离线 %s", reportNodeName(n.Name), n.Drops, reportDuration(n.OfflineSeconds)))
		}
	}

	lines = append(lines, "", "2. 流量与带宽")
	var up, down float64
	trafficNodes := 0
	var top *store.NodeDailyReport
	for i := range reports {
		n := &reports[i]
		if !n.TrafficObserved {
			continue
		}
		trafficNodes++
		up += float64(n.Upload)
		down += float64(n.Download)
		if top == nil || float64(n.Upload)+float64(n.Download) > float64(top.Upload)+float64(top.Download) {
			top = n
		}
	}
	if top == nil {
		lines = append(lines, "• 昨日总计：暂无流量采样")
	} else {
		lines = append(lines, fmt.Sprintf("• 昨日总计：↑ %s / ↓ %s", reportGB(up), reportGB(down)))
		if trafficNodes < len(reports) {
			lines = append(lines, fmt.Sprintf("• %d 个节点暂无流量采样", len(reports)-trafficNodes))
		}
		quota := "周期配额未设置或无记录"
		if top.CyclePercent != nil {
			quota = fmt.Sprintf("周期配额已用 %.1f%%", *top.CyclePercent)
		}
		lines = append(lines, fmt.Sprintf("• 消耗最高：%s（↑ %s / ↓ %s · %s）",
			reportNodeName(top.Name), reportGB(float64(top.Upload)), reportGB(float64(top.Download)), quota))
	}
	if bandwidth.Samples == 0 {
		lines = append(lines, "• 峰值带宽：暂无采样")
	} else {
		lines = append(lines, fmt.Sprintf("• 峰值带宽：%.1f Mbps · 近峰值持续约 %d 分钟（≥95%%，分钟采样）",
			bandwidth.BytesPerSecond*8/1e6, bandwidth.Minutes))
	}

	lines = append(lines, "", "3. 线路表现（UTC+8）")
	for _, segment := range []struct {
		title string
		prime bool
	}{{"黄金时段 20:00–23:00", true}, {"其他时段 00:00–20:00、23:00–24:00", false}} {
		lines = append(lines, segment.title)
		if len(reports) == 0 {
			lines = append(lines, "暂无节点记录")
		}
		for _, n := range reports {
			q := n.Other
			if segment.prime {
				q = n.Prime
			}
			lines = append(lines, reportNodeName(n.Name)+"  "+reportQualityText(q))
		}
	}
	return splitDailyReport(title, lines)
}

func reportGB(bytes float64) string {
	return fmt.Sprintf("%.1f GB", bytes/1e9)
}

func reportDuration(seconds float64) string {
	if seconds > 0 && seconds < 1 {
		return "<1 秒"
	}
	total := int((time.Duration(seconds * float64(time.Second))).Round(time.Second) / time.Second)
	hours, minutes, remainder := total/3600, total/60%60, total%60
	if hours > 0 {
		return fmt.Sprintf("%d 小时 %d 分 %d 秒", hours, minutes, remainder)
	}
	if minutes > 0 {
		return fmt.Sprintf("%d 分 %d 秒", minutes, remainder)
	}
	return fmt.Sprintf("%d 秒", remainder)
}

func reportNodeName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	runes := []rune(name)
	if len(runes) > 90 {
		return string(runes[:90]) + "…"
	}
	return name
}

func reportQualityText(q store.ReportLineQuality) string {
	if q.Samples == 0 {
		return "暂无采样"
	}
	latency, jitter := "—", "—"
	if q.Successful > 0 {
		latency = fmt.Sprintf("%.1f ms", q.LatencyMS)
	}
	if q.Pairs > 0 {
		jitter = fmt.Sprintf("%.1f ms", q.JitterMS)
	}
	return fmt.Sprintf("延迟 %s | 抖动 %s | 丢包 %.1f%%", latency, jitter, q.Loss)
}

// Two UTF-16 units per rune is a safe upper bound for Telegram message length.
func splitDailyReport(title string, lines []string) []string {
	const limit = 1950
	var parts []string
	part := title
	size := len([]rune(title))
	for _, line := range lines {
		length := len([]rune(line)) + 1
		if size+length > limit {
			parts = append(parts, part)
			part = title + "（续）"
			size = len([]rune(part))
		}
		part += "\n" + line
		size += length
	}
	return append(parts, part)
}
