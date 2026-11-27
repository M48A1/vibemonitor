package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type ReportSchedule struct {
	Enabled  bool   `json:"enabled"`
	Time     string `json:"time"`
	Timezone string `json:"timezone"`
}

var ErrInvalidReportSchedule = errors.New("自动生成时间或时区无效")

func (s *Store) ReportScheduleContext(ctx context.Context) (ReportSchedule, error) {
	schedule := ReportSchedule{Enabled: true, Time: "00:05", Timezone: "Asia/Shanghai"}
	err := s.sdb.reader().QueryRowContext(ctx, `SELECT enabled,local_time,timezone FROM report_schedule WHERE id=1`).
		Scan(&schedule.Enabled, &schedule.Time, &schedule.Timezone)
	if errors.Is(err, sql.ErrNoRows) {
		return schedule, nil
	}
	return schedule, err
}

func (s *Store) UpdateReportScheduleContext(ctx context.Context, schedule ReportSchedule) error {
	clock, err := time.Parse("15:04", schedule.Time)
	if err != nil || clock.Format("15:04") != schedule.Time || schedule.Timezone == "" || len(schedule.Timezone) > 64 {
		return ErrInvalidReportSchedule
	}
	if _, err := loadBillingLocation(schedule.Timezone); err != nil {
		return ErrInvalidReportSchedule
	}
	_, err = s.sdb.db.ExecContext(ctx, `INSERT INTO report_schedule(id,enabled,local_time,timezone) VALUES(1,?,?,?)
		ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled,local_time=excluded.local_time,timezone=excluded.timezone`,
		schedule.Enabled, schedule.Time, schedule.Timezone)
	return err
}

// ScheduledReportDate is the previous civil date of the most recent scheduled
// run. Comparing wall times also makes a skipped DST hour run once clocks pass it.
func (s ReportSchedule) ScheduledReportDate(now time.Time) (time.Time, error) {
	zone, err := loadBillingLocation(s.Timezone)
	if err != nil {
		return time.Time{}, err
	}
	local := now.In(zone)
	date := civilDate(local).AddDate(0, 0, -1)
	if local.Format("15:04") < s.Time {
		date = date.AddDate(0, 0, -1)
	}
	return date, nil
}
