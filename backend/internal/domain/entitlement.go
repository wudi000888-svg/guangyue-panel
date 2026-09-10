package domain

import (
	"errors"
	"time"
	_ "time/tzdata"
)

const CounterLimit int64 = 1 << 60

// Lifetime counters are never reset. A new quota period moves only baselines.
type QuotaMeter struct {
	InitialUpload     int64  `json:"initial_upload"`
	InitialDownload   int64  `json:"initial_download"`
	PeriodID          string `json:"period_id"`
	Start             int64  `json:"start"`
	End               int64  `json:"end"`
	PendingReset      bool   `json:"pending_reset"`
	Upload            int64  `json:"upload"`
	Download          int64  `json:"download"`
	UploadRemainder   int64  `json:"upload_remainder"`
	DownloadRemainder int64  `json:"download_remainder"`
	BaseUpload        int64  `json:"base_upload"`
	BaseDownload      int64  `json:"base_download"`
	RawBaseUpload     int64  `json:"raw_base_upload"`
	RawBaseDownload   int64  `json:"raw_base_download"`
}

type Entitlement struct {
	PlanID     string   `json:"plan_id"`
	Version    int      `json:"version"`
	Name       string   `json:"name"`
	GroupIDs   []string `json:"group_ids"`
	Cycle      string   `json:"cycle"`
	Timezone   string   `json:"timezone"`
	AssignedAt int64    `json:"assigned_at"`
	Revision   string   `json:"revision"`
}

func (u User) QuotaUpload() int64 {
	if u.Meter == nil {
		return u.Upload
	}
	return max(int64(0), u.Meter.Upload-u.Meter.BaseUpload)
}
func (u User) QuotaDownload() int64 {
	if u.Meter == nil {
		return u.Download
	}
	return max(int64(0), u.Meter.Download-u.Meter.BaseDownload)
}
func (u User) QuotaUsed() int64 { return u.QuotaUpload() + u.QuotaDownload() }

func (u *User) InitMeter(period string, now int64) {
	if u.Meter != nil {
		return
	}
	u.Meter = &QuotaMeter{PeriodID: period, Start: now, Upload: u.Upload, Download: u.Download, InitialUpload: u.Upload, InitialDownload: u.Download}
}

// Multiply a byte delta without overflowing the intermediate product; retain
// sub-byte residue so frequent sampling cannot increase rounding error.
func WeightedBytes(bytes, rate, remainder int64) (int64, int64, error) {
	if bytes < 0 || bytes > CounterLimit || rate < 0 || rate > 100000 || rate%10 != 0 || remainder < 0 || remainder >= 1000 {
		return 0, 0, errors.New("invalid weighted traffic")
	}
	whole, fraction := bytes/1000, (bytes%1000)*rate+remainder
	if rate > 0 && whole > (CounterLimit-fraction/1000)/rate {
		return 0, 0, errors.New("weighted traffic limit exceeded")
	}
	return whole*rate + fraction/1000, fraction % 1000, nil
}

func AddCounter(previous, delta int64) (int64, error) {
	if previous < 0 || delta < 0 || previous > CounterLimit-delta {
		return 0, errors.New("traffic limit exceeded")
	}
	return previous + delta, nil
}

func NextPeriod(start int64, cycle, zone string) (int64, error) {
	switch cycle {
	case "", "none":
		return 0, nil
	case "30d":
		return start + 30*86400, nil
	case "month":
		loc, err := time.LoadLocation(zone)
		if err != nil {
			return 0, err
		}
		t := time.Unix(start, 0).In(loc)
		return time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc).Unix(), nil
	default:
		return 0, errors.New("invalid quota cycle")
	}
}
