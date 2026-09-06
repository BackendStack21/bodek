package server

import (
	"errors"
	"strings"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		kind Kind
	}{
		{nil, KindUnknown},
		{errors.New(`cannot find "odek" on PATH — install odek`), KindMissingBinary},
		{errors.New("odek serve did not become ready: timeout"), KindNotReady},
		{errors.New("start odek serve: permission denied"), KindStartFail},
		{errors.New("allocate port: boom"), KindUnknown},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.kind {
			t.Errorf("Classify(%v) = %d, want %d", c.err, got, c.kind)
		}
	}
}

func TestDiagnoseMissingBinary(t *testing.T) {
	card := Diagnose(errors.New(`cannot find "odek" on PATH`), true)
	for _, want := range []string{"not on PATH", "--odek-bin", "no provider key", "retry: ⏎"} {
		if !strings.Contains(card, want) {
			t.Errorf("card missing %q:\n%s", want, card)
		}
	}
}

func TestDiagnoseNil(t *testing.T) {
	if Diagnose(nil, false) != "" {
		t.Fatal("nil error must not invent a card")
	}
}
