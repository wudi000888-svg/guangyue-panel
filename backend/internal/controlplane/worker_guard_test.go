package controlplane

import (
	"errors"
	"strings"
	"testing"
)

func TestSafeCallTurnsPanicIntoError(t *testing.T) {
	err := safeCall("fixture", func() error {
		var topics []string
		_ = topics[1][len(topics[1])-40:]
		return nil
	})
	if err == nil {
		t.Fatal("panic was not reported")
	}
	if e := safeCall("fixture", func() error { return errors.New("plain") }); e == nil || e.Error() != "plain" {
		t.Fatal("ordinary error changed", e)
	}
}

func TestWorkerReportKeepsLatestFailure(t *testing.T) {
	var r workerReport
	r.record("sweep", errors.New("rpc down"))
	r.record("sweep", errors.New("rpc down"))
	if s := r.snapshot()["sweep"]; s.Error != "rpc down" || s.Since == 0 {
		t.Fatal(s)
	}
	r.record("sweep", nil)
	if _, ok := r.snapshot()["sweep"]; ok {
		t.Fatal("recovered worker still reported")
	}
}

func TestCryptoTopicAddressRejectsShortTopic(t *testing.T) {
	valid := "0x" + strings.Repeat("0", 24) + strings.Repeat("ab", 20)
	for topic, want := range map[string]bool{valid: true, "0x1234": false, "": false, "0x" + strings.Repeat("f", 64): false} {
		if cryptoTopicAddress(topic) != want {
			t.Errorf("%q: want %v", topic, want)
		}
	}
}
