package telegram

import "time"

// Telegram clears the typing indicator after ~5s, so refresh it a bit sooner.
func timeAfter4s() <-chan time.Time { return time.After(4 * time.Second) }
