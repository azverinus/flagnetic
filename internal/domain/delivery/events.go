package delivery

import (
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/azverinus/flagnetic/eval"
	"github.com/azverinus/flagnetic/internal/shared/id"
)

// MaxEventString bounds every string of an event, in bytes.
const MaxEventString = 256

// Event marks the identifiers of events, chosen by the SDK.
type Event struct{}

// EventID identifies an event, a UUIDv7 the SDK chose so that a retry is
// recognised.
type EventID = id.Of[Event]

// SDKIdentity names the SDK that sent a request.
type SDKIdentity struct {
	Name     string
	Version  string
	Platform string
}

// SummaryRow counts the evaluations of one flag that served one variation
// for one reason.
type SummaryRow struct {
	Flag string
	// Variation is "" when no variation was served.
	Variation string
	Reason    string
	Count     int64
}

// SummarySource says who counted the evaluations of a summary row.
type SummarySource string

// The summary sources.
const (
	// SourceSDK is an SDK reporting the evaluations it made.
	SourceSDK SummarySource = "sdk"
	// SourceRemoteEvaluation is the server counting the remote evaluations
	// it answered, which a client SDK may count again from the answers it
	// received: reports keep the two apart so neither is added twice.
	SourceRemoteEvaluation SummarySource = "remote_evaluation"
)

// SummaryRecord is one row of an evaluation summary as stored: what was
// counted, by whom, and where.
type SummaryRecord struct {
	Source SummarySource
	// EventID is the summary's, zero for the server's own counts.
	EventID        EventID
	Key            SDKKey
	SDK            SDKIdentity
	AppVersion     string
	RulesetVersion int64
	PeriodStart    time.Time
	PeriodEnd      time.Time
	Row            SummaryRow
	ReceivedAt     time.Time
}

// UnknownSDK names an SDK identified neither by its User-Agent nor by what
// it posted.
const UnknownSDK = "unknown"

// userAgent is the User-Agent of a Flagnetic SDK:
// flagnetic-<sdk>/<version>, then its platform in parentheses.
var userAgent = regexp.MustCompile(`^(flagnetic-[^\s/()]+)/([^\s()]+)(?:\s+\(([^()]*)\))?`)

// ParseUserAgent returns the SDK a User-Agent names: a Flagnetic SDK's name,
// version and platform, each cut to MaxEventString bytes; anything else is
// UnknownSDK, without a version or platform.
func ParseUserAgent(ua string) SDKIdentity {
	m := userAgent.FindStringSubmatch(ua)
	if m == nil {
		return SDKIdentity{Name: UnknownSDK}
	}
	return SDKIdentity{Name: cut(m[1]), Version: cut(m[2]), Platform: cut(strings.TrimSpace(m[3]))}
}

// IdentifySDK returns the SDK a request comes from: the one its User-Agent
// names, else the one it posted itself, cut like a User-Agent's, else
// UnknownSDK. A browser cannot set User-Agent, so an SDK running in one
// names itself in what it posts: the context's standard attributes on
// evaluate, the envelope on events. The User-Agent wins when it names an
// SDK, since it is set by the SDK's own code and not by the app's context.
// What is posted without a name identifies nothing.
func IdentifySDK(userAgent string, posted SDKIdentity) SDKIdentity {
	if sdk := ParseUserAgent(userAgent); sdk.Name != UnknownSDK {
		return sdk
	}
	if posted.Name == "" {
		return SDKIdentity{Name: UnknownSDK}
	}
	return SDKIdentity{Name: cut(posted.Name), Version: cut(posted.Version), Platform: cut(posted.Platform)}
}

// The standard attributes by which a context names the SDK that sent it.
const (
	sdkAttribute        = "sdk"
	sdkVersionAttribute = "sdk_version"
	platformAttribute   = "platform"
)

// SDKOfContext returns the SDK a context's attributes name: sdk,
// sdk_version and platform, each only when a string.
func SDKOfContext(attributes map[string]any) SDKIdentity {
	text := func(key string) string {
		s, _ := attributes[key].(string)
		return s
	}
	return SDKIdentity{Name: text(sdkAttribute), Version: text(sdkVersionAttribute), Platform: text(platformAttribute)}
}

// cut returns s as valid UTF-8 of at most MaxEventString bytes without
// U+0000, whatever a client sent.
func cut(s string) string {
	s = strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", "�"), "�")
	for len(s) > MaxEventString {
		_, size := utf8.DecodeLastRuneInString(s)
		s = s[:len(s)-size]
	}
	return s
}

// RemoteEvaluator is who asked for remote evaluations: the key, the SDK it
// comes from, and the app version its context carried.
type RemoteEvaluator struct {
	Key        SDKKey
	SDK        SDKIdentity
	AppVersion string
}

// remoteGroup is what the remote evaluation counts are kept apart by.
type remoteGroup struct {
	key        SDKKeyID
	sdk        SDKIdentity
	appVersion string
	flag       string
	variation  string
	reason     eval.Reason
}

type remoteTally struct {
	evaluator      RemoteEvaluator
	count          int64
	first          time.Time
	rulesetVersion int64
}

// MaxRemoteCountGroups bounds the groups RemoteCounts holds between two
// flushes, so contexts carrying ever new app versions cannot grow a
// replica's memory: an evaluation that would open a group beyond it is not
// counted.
const MaxRemoteCountGroups = 10000

// RemoteCounts counts the remote evaluations a replica answers, per key,
// SDK, platform, app version, flag, variation and true reason, until they
// are taken as summary rows. The zero value counts nothing yet.
type RemoteCounts struct {
	groups map[remoteGroup]*remoteTally
	// Dropped counts the evaluations not counted because the groups were
	// full.
	Dropped int
}

// Add counts the results one remote evaluation answered at at, by their
// true reason, before any mask: reports count what targeting did, whoever
// asked.
func (c *RemoteCounts) Add(e RemoteEvaluator, rulesetVersion int64, results map[string]eval.Result, at time.Time) {
	if c.groups == nil {
		c.groups = map[remoteGroup]*remoteTally{}
	}
	for flag, r := range results {
		g := remoteGroup{key: e.Key.ID, sdk: e.SDK, appVersion: e.AppVersion, flag: flag, variation: r.Variation, reason: r.Reason}
		t, ok := c.groups[g]
		if !ok {
			if len(c.groups) >= MaxRemoteCountGroups {
				c.Dropped++
				continue
			}
			t = &remoteTally{evaluator: e, first: at}
			c.groups[g] = t
		}
		t.count++
		t.rulesetVersion = max(t.rulesetVersion, rulesetVersion)
		if at.Before(t.first) {
			t.first = at
		}
	}
}

// Len is how many groups are counted.
func (c *RemoteCounts) Len() int { return len(c.groups) }

// Records returns the counts as summary rows of source
// SourceRemoteEvaluation received at now, each over the period from its
// group's first evaluation to now.
func (c *RemoteCounts) Records(now time.Time) []SummaryRecord {
	out := make([]SummaryRecord, 0, len(c.groups))
	for g, t := range c.groups {
		out = append(out, SummaryRecord{
			Source: SourceRemoteEvaluation, Key: t.evaluator.Key, SDK: g.sdk, AppVersion: g.appVersion,
			RulesetVersion: t.rulesetVersion, PeriodStart: t.first, PeriodEnd: now, ReceivedAt: now,
			Row: SummaryRow{Flag: g.flag, Variation: g.variation, Reason: string(g.reason), Count: t.count},
		})
	}
	return out
}
