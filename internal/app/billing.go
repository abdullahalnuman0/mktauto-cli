package app

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/abdullahalnuman0/mktauto-cli/internal/api"
	"github.com/abdullahalnuman0/mktauto-cli/internal/auth"
	"github.com/abdullahalnuman0/mktauto-cli/internal/compose"
)

// ADR-087: the prepaid wallet. The terminal reads it (balance, history, a post's cost); paying is on the website.
// Prices come from the server only; the terminal never computes what is charged.

// usd: micro-USD as "$5.00", "$0.50", "$0.012", "$0.0025".
func usd(micros int64) string {
	sign := ""
	if micros < 0 {
		sign, micros = "-", -micros
	}
	s := strconv.FormatFloat(float64(micros)/1e6, 'f', 6, 64)
	s = strings.TrimRight(s, "0")
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 < 2 {
		s += strings.Repeat("0", 2-(len(s)-i-1))
	}
	return sign + "$" + s
}

var ledgerLabel = map[string]string{
	"CHARGE": "Post", "PLATFORM_COST": "Platform API cost", "FOLLOWUP": "Thread part / first comment", "AUTOMATION": "Terminal / API fee",
	"AI": "AI helper", "TOPUP": "Top-up", "BONUS": "Bonus", "WELCOME": "Welcome credit", "ADJUST": "Adjustment", "REFUND": "Refund",
}

func (e *Env) balance(ctx context.Context, args []string) error {
	fs := e.flags("balance", "balance [--days N] [--json]")
	days := fs.Int("days", 7, "history of the last N days (at most 30)")
	asJSON := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := e.client()
	if err != nil {
		return err
	}
	m, err := e.me(ctx, c)
	if err != nil {
		return err
	}
	b, err := c.Billing(ctx)
	if err != nil {
		return serverRefused(err)
	}
	if b == nil {
		e.printf("This MKTAuto server has no billing.\n")
		return nil
	}
	var lines []api.LedgerLine
	if !b.Exempt {
		if lines, err = c.Ledger(ctx, int(math.Max(1, math.Min(30, float64(*days))))); err != nil {
			return serverRefused(err)
		}
	}
	if *asJSON {
		return e.printJSON(map[string]any{"billing": b, "ledger": lines})
	}
	if b.Exempt {
		e.printf("%s Unlimited: this account has no billing.\n", green.Render("✓"))
		return nil
	}
	head := bold.Render("Balance " + usd(b.BalanceMicros))
	if b.Mode == "FROZEN" {
		head += " " + red.Render("· publishing paused (billing)")
	}
	e.printf("%s\n", head)
	e.printf("  %-20s %s\n", "Available", usd(b.AvailableMicros)+faint.Render(availableNote(b)))
	e.printf("  %-20s %s today · %s in 30 days\n", "Spent", usd(b.SpentTodayMicros), usd(b.Spent30dMicros))
	if b.DaysLeft != nil {
		e.printf("  %-20s about %s\n", "Lasts", plural(int(*b.DaysLeft), "day"))
	}
	if b.ScheduledEstimateMicros > 0 {
		e.printf("  %-20s %s\n", "Scheduled posts", "about "+usd(b.ScheduledEstimateMicros))
	}
	if b.DailyCapMicros != nil {
		e.printf("  %-20s %s\n", "Daily limit", usd(*b.DailyCapMicros))
	}
	if b.StorageQuotaBytes > 0 {
		e.printf("  %-20s %d of %d MB\n", "Media storage", b.StorageUsedBytes/(1<<20), b.StorageQuotaBytes/(1<<20))
	}
	if len(lines) > 0 {
		e.printf("%s\n", bold.Render(fmt.Sprintf("Last %s", plural(*days, "day"))))
		for _, l := range lines {
			at, _ := time.Parse(time.RFC3339Nano, l.CreatedAt)
			what := ledgerLabel[l.Kind]
			if l.Platform != "" {
				what += " · " + compose.PlatformLabel[l.Platform]
			}
			if l.PostTitle != "" {
				what += " · " + truncate(l.PostTitle, 34)
			}
			amount := usd(l.AmountMicros)
			if l.AmountMicros > 0 {
				amount = green.Render("+" + amount)
			}
			e.printf("  %s  %-62s %10s\n", faint.Render(at.Local().Format("02 Jan 15:04")), truncate(what, 62), amount)
		}
	}
	e.printf("%s\n", faint.Render("Top up on the website: "+m.WebURL+"/billing"))
	return nil
}

func availableNote(b *api.Billing) string {
	var parts []string
	if b.HeldMicros > 0 {
		parts = append(parts, usd(b.HeldMicros)+" set aside for posts going out")
	}
	if b.CreditLimitMicros > 0 {
		parts = append(parts, "includes "+usd(b.CreditLimitMicros)+" credit")
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// billingURL: prints (and opens) the website's Billing page.
func (e *Env) billing(ctx context.Context, args []string) error {
	c, err := e.client()
	if err != nil {
		return err
	}
	m, err := e.me(ctx, c)
	if err != nil {
		return err
	}
	url := m.WebURL + "/billing"
	if !e.Interactive || !auth.OpenBrowser(url) {
		e.printf("Billing (balance, top-ups, prices): %s\n", url)
	}
	return nil
}

// costLine: after a post is saved: what it will cost, and a warning when the balance doesn't cover it. Quiet for
// exempt accounts and servers without billing; never fails the command.
func (p *poster) costLine(ctx context.Context, postID string) {
	est, err := p.c.Estimate(ctx, postID)
	if err != nil || est == nil || est.Exempt || est.TotalMicros == 0 {
		return
	}
	line := "Estimated cost " + usd(est.TotalMicros)
	if est.AutomationMicros > 0 {
		line += faint.Render(" (includes the " + usd(est.AutomationMicros) + " terminal fee, once per post)")
	}
	p.env.printf("  %s\n", line)
	if est.AvailableMicros != nil && *est.AvailableMicros < est.TotalMicros {
		m, _ := p.env.me(ctx, p.c)
		where := "on the website"
		if m != nil {
			where = m.WebURL + "/billing"
		}
		p.env.printf("  %s\n", yellow.Render("! Your balance ("+usd(*est.AvailableMicros)+") doesn't cover it: accounts it can't pay for fail with \"Not enough balance\". Top up: "+where))
	}
}
