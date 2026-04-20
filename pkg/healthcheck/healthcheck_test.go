package healthcheck

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"
)

func TestHealthyAcceptsFreshEvent(t *testing.T) {
	g := NewWithT(t)

	watchdog := &Watchdog{}
	watchdog.MarkSeen(time.Now())

	g.Expect(watchdog.Healthy(time.Second)).To(Succeed())
}

func TestHealthyRejectsStaleEvent(t *testing.T) {
	g := NewWithT(t)

	watchdog := &Watchdog{}
	watchdog.MarkSeen(time.Now().Add(-time.Minute))

	g.Expect(watchdog.Healthy(10 * time.Second)).NotTo(Succeed())
}
