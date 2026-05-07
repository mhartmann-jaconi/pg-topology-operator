package cluster

import (
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/config"
	"github.com/jaconi-io/ipoque-pg-webhook/pkg/events"
	"github.com/spf13/viper"
)

const (
	leaderLabel = "lead"
	regionLabel = "region"
	zoneLabel   = "zone"
)

func setup() {
	viper.Set(config.LabelLeader, leaderLabel)
	viper.Set(config.LabelRegion, regionLabel)
	viper.Set(config.LabelZone, zoneLabel)
}

func cleanup() {
	ClearState()
	events.SetListenersForRegionChange() // reset global listeners to prevent cross-test leakage
	viper.Reset()
}
