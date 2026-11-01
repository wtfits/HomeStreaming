package stream

import (
	"time"

	"github.com/wtfits/homestreaming/internal/db"
)

// ApplyProgress updates user data for a reported position (DESIGN §7.5).
// It returns true when this report marked the item played. countPlay controls
// whether play_count may be incremented (once per play session).
func ApplyProgress(u *db.UserItemData, positionMs, runtimeMs int64, thresholdPct int, ended, countPlay bool, now time.Time) bool {
	if thresholdPct <= 0 {
		thresholdPct = 92
	}
	u.LastPlayedAt = db.FormatTime(now)
	minPos := int64(5000)
	if runtimeMs > 0 && runtimeMs*2/100 > minPos {
		minPos = runtimeMs * 2 / 100
	}
	if ended || (runtimeMs > 0 && positionMs*100 >= runtimeMs*int64(thresholdPct)) {
		u.Played = true
		u.PositionMs = 0
		if countPlay {
			u.PlayCount++
		}
		return true
	}
	if positionMs <= minPos {
		u.PositionMs = 0
		return false
	}
	u.PositionMs = positionMs
	if u.Played {
		// Re-watching a played item: it is in progress again.
		u.Played = false
	}
	return false
}
