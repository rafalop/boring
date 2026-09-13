package main

import (
	"fmt"
	"time"

	"github.com/alebeck/boring/internal/log"
	"github.com/alebeck/boring/internal/vpn"
)

func status(v *vpn.Desc) string {
	switch v.Status {
	case vpn.Closed:
		return log.Red + "closed" + log.Reset
	case vpn.Reconn:
		return log.Yellow + "reconn" + log.Reset
	}

	// Vpn is up, show uptime
	since := time.Since(v.LastConn)
	days := int(since / (24 * time.Hour))
	hours := int(since/time.Hour) % 24
	mins := int(since/time.Minute) % 60
	secs := int(since/time.Second) % 60
	var str string
	if days > 0 {
		str = fmt.Sprintf("%02dd%02dh", days, hours)
	} else if hours > 0 {
		str = fmt.Sprintf("%02dh%02dm", hours, mins)
	} else {
		str = fmt.Sprintf("%02dm%02ds", mins, secs)
	}
	return log.Bold + log.Green + str + log.Reset
}
