package app

import (
	"strings"
	"testing"
)

func TestUSD(t *testing.T) {
	for micros, want := range map[int64]string{5_000_000: "$5.00", 500_000: "$0.50", 12_000: "$0.012", 2_500: "$0.0025", -1_200_000: "-$1.20", 0: "$0.00"} {
		if got := usd(micros); got != want {
			t.Errorf("usd(%d) = %s, want %s", micros, got, want)
		}
	}
}

func TestBalanceAndPostCost(t *testing.T) {
	_, dir := setup(t)
	login(t, dir)
	code, out := runCLI(t, dir, "balance")
	if code != 0 || !strings.Contains(out, "Balance $0.48") || !strings.Contains(out, "$0.02 set aside") || !strings.Contains(out, "about 120 days") ||
		!strings.Contains(out, "Post · Instagram · Launch reel") || !strings.Contains(out, "+$10.00") || !strings.Contains(out, "/billing") {
		t.Errorf("balance: %s", out)
	}
	if code, out := runCLI(t, dir, "balance", "--json"); code != 0 || !strings.Contains(out, `"balanceMicros": 480000`) {
		t.Errorf("balance --json: %s", out)
	}
	// After saving a post: its cost, the terminal fee once, and a warning when the balance doesn't cover it.
	code, out = runCLI(t, dir, "post", "--project", "Memoshot", "--accounts", "facebook", "--caption", "Paid")
	if code != 0 || !strings.Contains(out, "Estimated cost $0.011") || !strings.Contains(out, "$0.007 terminal fee") || !strings.Contains(out, "doesn't cover it") {
		t.Errorf("post cost: %s", out)
	}
}
