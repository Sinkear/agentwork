package service

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// allCodes is the single source of truth for the error codes this package
// exports. The real frontend (AI_Shell_WEB) does NOT hard-code this list —
// it looks up codes dynamically in the remote term.json, so adding a code
// only requires updating this list AND the remote term.json (via the CDN
// approval flow). The self-test frontend (web/lib/codes.ts) mirrors this
// list by hand for documentation. TestErrorCodesExported guards the
// backend list itself (uniqueness, non-empty). TestCodesMatchRemoteTermJSON
// guards that every code has a corresponding entry in the remote term.json
// — this is the cross-layer drift guard.
var allCodes = []string{
	CodeValidation,
	CodeNotFound,
	CodeDomainHasGoals,
	CodeRuntimeHasAgents,
	CodeAgentLeadsSquad,
	CodeSkillSelected,
	CodeAgentNameExists,
	CodeSkillNameExists,
	CodeRuntimeNameExists,
	CodeDomainNameExists,
	CodeGoalNotActivatable,
	CodeGoalNotReopenable,
	CodeGoalNotInReview,
	CodeSquadNameExists,
	CodeFieldRequired,
	CodeAgentHasGoals,
	CodeAgentHasSchedules,
	CodeAgentInSquads,
	CodeAgentHasRunningRuns,
	CodeAgentHandlesIssues,
	CodeDomainHasSchedules,
	CodeSquadHasGoals,
	CodeSquadHasSchedules,
	CodeSquadHandlesIssues,
	CodeScheduleBuiltIn,
	CodeTemplateSourceUnset,
	CodeTemplateFetchFailed,
	CodeTemplateInvalid,
	CodeTemplateNotFound,
	CodeRepoCreateFailed,
}

func TestErrorCodesExported(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range allCodes {
		if c == "" {
			t.Fatal("empty code in allCodes — every entry must be a non-empty string")
		}
		if seen[c] {
			t.Errorf("duplicate error code %q — codes must be unique", c)
		}
		seen[c] = true
	}
	if len(allCodes) == 0 {
		t.Fatal("no error codes exported")
	}
}

// remoteTermURLs are the CDN-hosted localized message files. The frontend
// looks up AW.xxxxxxxx codes here for a localized message; this test asserts
// every backend code has a corresponding entry in BOTH locales so the
// frontend never gets a miss regardless of locale.
var remoteTermURLs = []string{
	"https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/zh-cn/term.json",
	"https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/en-us/term.json",
}

// skipCDNDriftEnv is the operator escape hatch for a run that cannot reach the
// CDN (offline dev box, degraded CI). Only the exact value "1" counts — v1
// tested `!= ""`, so AGENTWORK_SKIP_CDN_DRIFT=0 also muted the guard.
const skipCDNDriftEnv = "AGENTWORK_SKIP_CDN_DRIFT"

// cdnDriftForceSkip reports whether the operator override is on. The bind point
// for the "== \"1\"" rule; TestCDNDriftEnvSwitch pins it.
func cdnDriftForceSkip() bool { return os.Getenv(skipCDNDriftEnv) == "1" }

// cdnDriftAllowlistExpiry is the instant the grace window below closes: the
// end of 2026-12-31 (UTC). A code's entry expires at that instant — at
// exactly this time the guard FAILS, so the window never silently extends.
var cdnDriftAllowlistExpiry = time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)

// cdnDriftAllowlist pins the ONLY codes allowed to be missing from the remote
// term.json, each with the instant its individual grace window closes. The
// template codes AW.10000025–30 shipped in the backend before the CDN
// approval flow carried their term.json entries; before this allowlist the
// guard muted EVERY missing code (t.Skipf), which hid genuine drift. Now each
// entry is a time-boxed exception, not a blanket mute:
//
//   - a missing code ABSENT from this map          → FAIL (unknown drift)
//   - a missing code PRESENT, now < its expiry     → SKIP (known backlog)
//   - a missing code PRESENT, now >= its expiry    → FAIL (renew deliberately
//     through the CDN approval flow; never extend blindly)
//   - AGENTWORK_SKIP_CDN_DRIFT=1                   → SKIP regardless (operator
//     override for an offline/degraded-CDN run)
//
// Extending this map is a deliberate act: add the code with an explicit date,
// and only for a window in which the approval flow is expected to land.
var cdnDriftAllowlist = map[string]time.Time{
	CodeScheduleBuiltIn:     cdnDriftAllowlistExpiry, // AW.10000025
	CodeTemplateSourceUnset: cdnDriftAllowlistExpiry, // AW.10000026
	CodeTemplateFetchFailed: cdnDriftAllowlistExpiry, // AW.10000027
	CodeTemplateInvalid:     cdnDriftAllowlistExpiry, // AW.10000028
	CodeTemplateNotFound:    cdnDriftAllowlistExpiry, // AW.10000029
	CodeRepoCreateFailed:    cdnDriftAllowlistExpiry, // AW.10000030
}

// cdnDriftDecision is the verdict of classifyCDNDrift. Skip=true means the
// drift guard should t.Skip (every missing code is known and still inside its
// grace window, or the operator forced a skip); Skip=false means the test must
// FAIL and Reason says why (unknown/expired codes, then Agentwork has real
// backend↔CDN drift).
type cdnDriftDecision struct {
	Skip   bool
	Reason string
}

// classifyCDNDrift maps "which codes are missing from the CDN term.json" to the
// guard's verdict. It is PURE — no network, no clock, no environment — so every
// branch is table-testable (TestClassifyCDNDrift); TestCodesMatchRemoteTermJSON
// only supplies the live inputs for the current locale.
func classifyCDNDrift(missing []string, now time.Time, allowlist map[string]time.Time, forceSkip bool) cdnDriftDecision {
	if len(missing) == 0 {
		return cdnDriftDecision{Skip: false, Reason: "no codes missing"}
	}
	// Explicit operator override wins over the allowlist (an offline run cannot
	// introspect the CDN at all). It is NOT a silent default.
	if forceSkip {
		return cdnDriftDecision{Skip: true, Reason: "AGENTWORK_SKIP_CDN_DRIFT=1 — operator override"}
	}
	var unknown, expired []string
	for _, code := range missing {
		expiry, ok := allowlist[code]
		if !ok {
			unknown = append(unknown, code)
			continue
		}
		if !now.Before(expiry) {
			expired = append(expired, code)
		}
	}
	if len(unknown) > 0 {
		return cdnDriftDecision{Skip: false,
			Reason: "missing code(s) not on the allowlist — real drift: " + strings.Join(unknown, ", ")}
	}
	if len(expired) > 0 {
		return cdnDriftDecision{Skip: false,
			Reason: "allowlist grace window expired for: " + strings.Join(expired, ", ")}
	}
	return cdnDriftDecision{Skip: true,
		Reason: "all missing codes are allowlisted and unexpired (through " + cdnDriftAllowlistExpiry.Format("2006-01-02") + " UTC)"}
}

// termURLLocale extracts the locale segment from a remoteTermURLs entry
// (…/zh-cn/term.json → zh-cn) so FAIL messages name the locale, not just the
// URL.
func termURLLocale(url string) string {
	parts := strings.Split(strings.TrimSuffix(url, "/term.json"), "/")
	return parts[len(parts)-1]
}

// TestCodesMatchRemoteTermJSON guards that every code in allCodes has a
// corresponding entry in every remote term.json (zh-cn and en-us). Semantics
// (single source of truth: classifyCDNDrift):
//
//   - network unreachable / non-200 / malformed JSON → t.Skip: an external,
//     transient CDN-side anomaly outside this repo's control.
//   - missing codes → FAIL by default (t.Errorf, listing locale + codes), with
//     the only exceptions being a code on cdnDriftAllowlist whose expiry has
//     not passed, or the explicit AGENTWORK_SKIP_CDN_DRIFT=1 operator override.
//     This is the difference between a drift DETECTOR and a drift silencer.
//
// Developers cannot edit term.json directly — a missing code is added via the
// CDN approval flow. The 10s timeout keeps the test from hanging on a slow or
// half-open CDN.
func TestCodesMatchRemoteTermJSON(t *testing.T) {
	forceSkip := cdnDriftForceSkip()
	client := &http.Client{Timeout: 10 * time.Second}
	for _, url := range remoteTermURLs {
		locale := termURLLocale(url)
		t.Run(locale, func(t *testing.T) {
			resp, err := client.Get(url)
			if err != nil {
				t.Skipf("cannot reach CDN — skip drift guard (external/transient): %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Skipf("CDN returned %d — skip drift guard (external/transient)", resp.StatusCode)
			}
			var terms map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&terms); err != nil {
				// Truncated JSON / a gateway error page served with 200 is a
				// CDN-side anomaly, not a backend↔term.json drift — same skip
				// semantics as unreachable/non-200.
				t.Skipf("CDN returned malformed body — skip drift guard (external/transient): %v", err)
			}
			var missing []string
			for _, c := range allCodes {
				if _, ok := terms[c]; !ok {
					missing = append(missing, c)
				}
			}
			sort.Strings(missing) // deterministic message regardless of allCodes order
			d := classifyCDNDrift(missing, time.Now(), cdnDriftAllowlist, forceSkip)
			if len(missing) == 0 {
				return // full sync — nothing to report
			}
			if d.Skip {
				// Time-boxed: the skip message always carries the full missing
				// list so drift stays diagnosable; re-run with
				// `-run TestCodesMatchRemoteTermJSON` once the window closes.
				t.Skipf("CDN term.json drift is excused — %s — locale %s: %d code(s) missing: %s",
					d.Reason, locale, len(missing), strings.Join(missing, ", "))
			}
			t.Errorf("CDN term.json is out of sync with the backend code list — %s — locale %s: %d code(s) missing: %s\n"+
				"Add each code to term.json via the CDN approval flow (developers cannot edit it directly). "+
				"If this is a known, time-boxed backlog, add the code to cdnDriftAllowlist WITH an explicit expiry; "+
				"set %s=1 only for an offline/degraded-CDN run.",
				d.Reason, locale, len(missing), strings.Join(missing, ", "), skipCDNDriftEnv)
		})
	}
}

// TestClassifyCDNDrift table-tests every branch of the drift classifier:
// full sync, allowlisted-but-live, allowlist expired, unknown (real) drift,
// mixed unknown+expired, empty allowlist, and the operator override.
func TestClassifyCDNDrift(t *testing.T) {
	t.Parallel()
	before := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	expiry := time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC)
	allow := map[string]time.Time{
		"AW.10000025": expiry,
		"AW.10000026": expiry,
	}
	cases := []struct {
		name       string
		missing    []string
		now        time.Time
		allowlist  map[string]time.Time
		forceSkip  bool
		wantSkip   bool
		wantReason string // substring the Reason must contain
	}{
		{
			name: "full sync — no missing codes", missing: nil, now: before, allowlist: allow,
			wantSkip: false, wantReason: "no codes missing",
		},
		{
			name: "all missing allowlisted, unexpired", missing: []string{"AW.10000025", "AW.10000026"},
			now: before, allowlist: allow, wantSkip: true, wantReason: "allowlisted and unexpired",
		},
		{
			name: "allowlisted one second before expiry", missing: []string{"AW.10000025"},
			now: expiry.Add(-time.Second), allowlist: allow, wantSkip: true, wantReason: "allowlisted and unexpired",
		},
		{
			name: "allowlisted exactly at expiry instant — expired", missing: []string{"AW.10000025"},
			now: expiry, allowlist: allow, wantSkip: false, wantReason: "expired for: AW.10000025",
		},
		{
			name: "allowlisted after expiry — expired", missing: []string{"AW.10000025"},
			now: expiry.Add(24 * time.Hour), allowlist: allow, wantSkip: false, wantReason: "expired",
		},
		{
			name: "unknown code — real drift", missing: []string{"AW.10000025", "AW.10000031"},
			now: before, allowlist: allow, wantSkip: false, wantReason: "not on the allowlist — real drift: AW.10000031",
		},
		{
			name: "empty allowlist — every code unknown", missing: []string{"AW.10000025"},
			now: before, allowlist: nil, wantSkip: false, wantReason: "not on the allowlist",
		},
		{
			name: "unknown wins over expired", missing: []string{"AW.10000025", "AW.10000031"},
			now: expiry.Add(24 * time.Hour), allowlist: allow, wantSkip: false, wantReason: "not on the allowlist",
		},
		{
			name: "env override skips unknown drift", missing: []string{"AW.10000031"},
			now: before, allowlist: allow, forceSkip: true, wantSkip: true, wantReason: "operator override",
		},
		{
			name: "env override skips expired allowlist", missing: []string{"AW.10000025"},
			now: expiry.Add(24 * time.Hour), allowlist: allow, forceSkip: true,
			wantSkip: true, wantReason: "operator override",
		},
		{
			name: "env override with empty allowlist", missing: []string{"AW.10000031"},
			now: before, allowlist: nil, forceSkip: true, wantSkip: true, wantReason: "operator override",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyCDNDrift(tc.missing, tc.now, tc.allowlist, tc.forceSkip)
			if got.Skip != tc.wantSkip {
				t.Fatalf("Skip = %v, want %v (reason: %s)", got.Skip, tc.wantSkip, got.Reason)
			}
			if !strings.Contains(got.Reason, tc.wantReason) {
				t.Fatalf("Reason = %q, want it to contain %q", got.Reason, tc.wantReason)
			}
		})
	}
}

// TestCDNDriftEnvSwitch pins the env-switch semantics: ONLY the exact value
// "1" mutes the guard (v1's `!= ""` would also mute it for "0"/"true"/"no").
func TestCDNDriftEnvSwitch(t *testing.T) {
	for _, v := range []string{"", "0", "true", "yes", " 1"} {
		t.Setenv(skipCDNDriftEnv, v)
		if cdnDriftForceSkip() {
			t.Fatalf("%s=%q must NOT be read as on", skipCDNDriftEnv, v)
		}
	}
	t.Setenv(skipCDNDriftEnv, "1")
	if !cdnDriftForceSkip() {
		t.Fatalf("%s=1 must be read as on", skipCDNDriftEnv)
	}
	// The allowlist itself must actually pin AW.10000025–30 (the template
	// backlog), no more and no less.
	want := map[string]bool{
		"AW.10000025": true,
		"AW.10000026": true,
		"AW.10000027": true,
		"AW.10000028": true,
		"AW.10000029": true,
		"AW.10000030": true,
	}
	if len(cdnDriftAllowlist) != len(want) {
		t.Fatalf("allowlist has %d entries, want %d", len(cdnDriftAllowlist), len(want))
	}
	for code, exp := range cdnDriftAllowlist {
		if !want[code] {
			t.Errorf("unexpected allowlisted code %q", code)
		}
		if exp != cdnDriftAllowlistExpiry {
			t.Errorf("allowlisted code %q expires at %v, want %v", code, exp, cdnDriftAllowlistExpiry)
		}
	}
}

// TestTermURLLocale guards the locale extraction used in FAIL/skip messages.
func TestTermURLLocale(t *testing.T) {
	cases := map[string]string{
		"https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/zh-cn/term.json": "zh-cn",
		"https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/en-us/term.json": "en-us",
	}
	for url, want := range cases {
		if got := termURLLocale(url); got != want {
			t.Errorf("termURLLocale(%q) = %q, want %q", url, got, want)
		}
	}
}
