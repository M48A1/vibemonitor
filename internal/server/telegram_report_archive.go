package server

import (
	"context"
	"fmt"
	"time"

	"vibemonitor/internal/store"
)

const reportCommandUsage = "回复 2026.11.10 查询一天，或回复 2026.11.10 2026.11.15 查询日期范围（包含首尾）。也可以在日期前加 /report，日期支持 YYYY-MM-DD。"

func parseReportDate(value string) (time.Time, error) {
	date, err := time.Parse(time.DateOnly, value)
	if err == nil {
		return date, nil
	}
	return time.Parse("2006.01.02", value)
}

func (s *Server) replyArchivedReportCommand(ctx context.Context, now time.Time, args []string, send func(string) error) error {
	if len(args) < 1 || len(args) > 2 {
		return send(reportCommandUsage)
	}
	first, err := parseReportDate(args[0])
	if err != nil {
		return send("日期格式无效。\n" + reportCommandUsage)
	}
	last := first
	if len(args) == 2 {
		last, err = parseReportDate(args[1])
		if err != nil {
			return send("日期格式无效。\n" + reportCommandUsage)
		}
	}
	if last.Before(first) {
		return send("结束日期要等于或晚于开始日期。\n" + reportCommandUsage)
	}
	reports, err := s.store.ArchivedDailyReportsContext(ctx, first, last)
	if err != nil {
		_ = send("暂时无法读取日报，请稍后重试。")
		return err
	}
	if len(reports) == 0 {
		return send("所选日期没有已存档的日报。")
	}
	sent, incomplete := 0, 0
	for _, report := range reports {
		date, err := time.Parse(time.DateOnly, report.Date)
		if err != nil {
			return err
		}
		if !s.store.ArchivedDailyReportComplete(date, now, report) {
			incomplete++
			continue
		}
		if err := sendArchivedDailyReport(report, send); err != nil {
			return err
		}
		sent++
	}
	requested := int((last.Unix()-first.Unix())/86400) + 1
	if incomplete > 0 {
		if requested == 1 {
			return send("所选日期的节点日报尚未汇总完整。")
		}
		return send(fmt.Sprintf("已发送 %d 天的日报，另有 %d 天尚未汇总完整、%d 天没有存档。", sent, incomplete, requested-len(reports)))
	}
	if sent < requested {
		return send(fmt.Sprintf("该范围内已发送 %d 天的日报，另有 %d 天没有存档。", sent, requested-sent))
	}
	return nil
}

func sendArchivedDailyReport(report store.ArchivedDailyReport, send func(string) error) error {
	for _, part := range dailyReportMessages(report.Date, report.Date, report.Nodes, nil, report.Bandwidth) {
		if err := send(part); err != nil {
			return err
		}
	}
	return nil
}
