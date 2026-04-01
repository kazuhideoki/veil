package usecase

import "time"

func currentTime(now func() time.Time) time.Time {
	if now == nil {
		return time.Now().UTC()
	}

	return now().UTC()
}
