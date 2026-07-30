package ldai

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/launchdarkly/go-sdk-common/v4/ldcontext"
	"github.com/launchdarkly/go-sdk-common/v4/ldvalue"
)

const graphTrackerContentionGoroutines = 20

// runGraphTrackerContention starts n goroutines that wait on a shared start signal, then
// each invoke fn once. Using a barrier keeps the race window open instead of serializing
// launches in a loop.
func runGraphTrackerContention(n int, fn func()) {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			fn()
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
}

func makeGraphTracker(events *mockEvents, variationKey string) *GraphTracker {
	return newGraphTracker(
		events,
		"run-1",
		"my-graph",
		variationKey,
		2,
		ldcontext.New("user"),
		events.log.Loggers,
	)
}

func countEventsNamed(events *mockEvents, name string) int {
	n := 0
	for _, e := range events.events {
		if e.name == name {
			n++
		}
	}
	return n
}

func firstEventNamed(events *mockEvents, name string) *trackEvent {
	for i := range events.events {
		if events.events[i].name == name {
			return &events.events[i]
		}
	}
	return nil
}

func TestGraphTracker_TrackInvocation_AtMostOnceAndMutualExclusion(t *testing.T) {
	t.Run("success then failure drops failure", func(t *testing.T) {
		events := newMockEvents()
		tracker := makeGraphTracker(events, "var-1")

		require.NoError(t, tracker.TrackInvocationSuccess())
		require.NoError(t, tracker.TrackInvocationFailure())

		assert.Equal(t, 1, countEventsNamed(events, graphInvocationSuccess))
		assert.Equal(t, 0, countEventsNamed(events, graphInvocationFailure))
		summary := tracker.GetSummary()
		require.True(t, summary.Success.IsSome())
		assert.True(t, summary.Success.Unwrap())
	})

	t.Run("failure then success drops success", func(t *testing.T) {
		events := newMockEvents()
		tracker := makeGraphTracker(events, "var-1")

		require.NoError(t, tracker.TrackInvocationFailure())
		require.NoError(t, tracker.TrackInvocationSuccess())

		assert.Equal(t, 1, countEventsNamed(events, graphInvocationFailure))
		assert.Equal(t, 0, countEventsNamed(events, graphInvocationSuccess))
		summary := tracker.GetSummary()
		require.True(t, summary.Success.IsSome())
		assert.False(t, summary.Success.Unwrap())
	})

	t.Run("duplicate success dropped", func(t *testing.T) {
		events := newMockEvents()
		tracker := makeGraphTracker(events, "")

		require.NoError(t, tracker.TrackInvocationSuccess())
		require.NoError(t, tracker.TrackInvocationSuccess())
		assert.Equal(t, 1, countEventsNamed(events, graphInvocationSuccess))
	})
}

func TestGraphTracker_TrackDuration_AtMostOnceAndNonFinite(t *testing.T) {
	t.Run("records once", func(t *testing.T) {
		events := newMockEvents()
		tracker := makeGraphTracker(events, "")

		require.NoError(t, tracker.TrackDuration(12.5))
		require.NoError(t, tracker.TrackDuration(99))

		assert.Equal(t, 1, countEventsNamed(events, graphDurationTotal))
		ev := firstEventNamed(events, graphDurationTotal)
		require.NotNil(t, ev)
		assert.Equal(t, 12.5, ev.metricValue)
		assert.Equal(t, 12.5, tracker.GetSummary().DurationMs.Unwrap())
	})

	t.Run("non-finite does not burn slot", func(t *testing.T) {
		events := newMockEvents()
		tracker := makeGraphTracker(events, "")

		require.NoError(t, tracker.TrackDuration(math.NaN()))
		require.NoError(t, tracker.TrackDuration(math.Inf(1)))
		require.NoError(t, tracker.TrackDuration(math.Inf(-1)))
		assert.Equal(t, 0, countEventsNamed(events, graphDurationTotal))
		assert.True(t, tracker.GetSummary().DurationMs.IsNone())

		require.NoError(t, tracker.TrackDuration(7))
		assert.Equal(t, 1, countEventsNamed(events, graphDurationTotal))
		assert.Equal(t, 7.0, tracker.GetSummary().DurationMs.Unwrap())
	})
}

func TestGraphTracker_TrackTotalTokens_AtMostOnce(t *testing.T) {
	events := newMockEvents()
	tracker := makeGraphTracker(events, "")

	require.NoError(t, tracker.TrackTotalTokens(TokenUsage{Total: 10, Input: 6, Output: 4}))
	require.NoError(t, tracker.TrackTotalTokens(TokenUsage{Total: 99}))

	assert.Equal(t, 1, countEventsNamed(events, graphTotalTokens))
	ev := firstEventNamed(events, graphTotalTokens)
	require.NotNil(t, ev)
	assert.Equal(t, float64(10), ev.metricValue)
	assert.Equal(t, 10, tracker.GetSummary().Tokens.Unwrap().Total)
}

func TestGraphTracker_TrackPath_AtMostOnceAndEmpty(t *testing.T) {
	t.Run("records once with path data", func(t *testing.T) {
		events := newMockEvents()
		tracker := makeGraphTracker(events, "var-1")

		require.NoError(t, tracker.TrackPath([]string{"a", "b"}))
		require.NoError(t, tracker.TrackPath([]string{"x"}))

		assert.Equal(t, 1, countEventsNamed(events, graphPath))
		ev := firstEventNamed(events, graphPath)
		require.NotNil(t, ev)
		assert.Equal(t, float64(1), ev.metricValue)
		assert.Equal(t, "a", ev.data.GetByKey("path").GetByIndex(0).StringValue())
		assert.Equal(t, "b", ev.data.GetByKey("path").GetByIndex(1).StringValue())
		assert.Equal(t, []string{"a", "b"}, tracker.GetSummary().Path)
	})

	t.Run("nil and empty do not burn slot", func(t *testing.T) {
		events := newMockEvents()
		tracker := makeGraphTracker(events, "")

		require.NoError(t, tracker.TrackPath(nil))
		require.NoError(t, tracker.TrackPath([]string{}))
		assert.Equal(t, 0, countEventsNamed(events, graphPath))
		assert.Nil(t, tracker.GetSummary().Path)

		require.NoError(t, tracker.TrackPath([]string{"only"}))
		assert.Equal(t, 1, countEventsNamed(events, graphPath))
		assert.Equal(t, []string{"only"}, tracker.GetSummary().Path)
	})
}

func TestGraphTracker_EdgeMethods_MultiFire(t *testing.T) {
	events := newMockEvents()
	tracker := makeGraphTracker(events, "var-1")

	require.NoError(t, tracker.TrackRedirect("a", "b"))
	require.NoError(t, tracker.TrackRedirect("a", "c"))
	require.NoError(t, tracker.TrackHandoffSuccess("a", "b"))
	require.NoError(t, tracker.TrackHandoffSuccess("b", "c"))
	require.NoError(t, tracker.TrackHandoffFailure("a", "x"))
	require.NoError(t, tracker.TrackHandoffFailure("a", "y"))

	assert.Equal(t, 2, countEventsNamed(events, graphRedirect))
	assert.Equal(t, 2, countEventsNamed(events, graphHandoffSuccess))
	assert.Equal(t, 2, countEventsNamed(events, graphHandoffFailure))

	redirect := firstEventNamed(events, graphRedirect)
	require.NotNil(t, redirect)
	assert.Equal(t, "a", redirect.data.GetByKey("sourceKey").StringValue())
	assert.Equal(t, "b", redirect.data.GetByKey("redirectedTarget").StringValue())
	assert.Equal(t, "my-graph", redirect.data.GetByKey("graphKey").StringValue())
	assert.Equal(t, "var-1", redirect.data.GetByKey("variationKey").StringValue())
}

func TestGraphTracker_EdgeMethods_BlankKeysSkipped(t *testing.T) {
	events := newMockEvents()
	tracker := makeGraphTracker(events, "")

	require.NoError(t, tracker.TrackRedirect("", "b"))
	require.NoError(t, tracker.TrackRedirect("a", "  "))
	require.NoError(t, tracker.TrackHandoffSuccess(" ", "b"))
	require.NoError(t, tracker.TrackHandoffFailure("a", ""))

	assert.Empty(t, events.events)
}

func TestGraphTracker_BaseTrackDataOmitsEmptyVariationKey(t *testing.T) {
	events := newMockEvents()
	tracker := makeGraphTracker(events, "")
	require.NoError(t, tracker.TrackInvocationSuccess())

	ev := firstEventNamed(events, graphInvocationSuccess)
	require.NotNil(t, ev)
	assert.Equal(t, "run-1", ev.data.GetByKey("runId").StringValue())
	assert.Equal(t, "my-graph", ev.data.GetByKey("graphKey").StringValue())
	assert.Equal(t, 2, ev.data.GetByKey("version").IntValue())
	assert.Equal(t, ldvalue.Null(), ev.data.GetByKey("variationKey"))
}

func TestGraphTracker_ResumptionToken_RoundTrip(t *testing.T) {
	events := newMockEvents()
	original := makeGraphTracker(events, "var-abc")
	token := original.ResumptionToken()

	raw, err := base64.RawURLEncoding.DecodeString(token)
	require.NoError(t, err)
	// Verify canonical field order.
	assert.Contains(t, string(raw), `"runId":"run-1","graphKey":"my-graph","variationKey":"var-abc","version":2`)

	sdk := newMultiFlagMockSDK(nil)
	client, err := NewClient(sdk)
	require.NoError(t, err)

	reconstructed, err := client.CreateGraphTracker(token, ldcontext.New("other"))
	require.NoError(t, err)
	require.NotNil(t, reconstructed)

	assert.Equal(t, original.ResumptionToken(), reconstructed.ResumptionToken())
	require.NoError(t, reconstructed.TrackInvocationSuccess())
	ev := sdk.events[len(sdk.events)-1]
	assert.Equal(t, graphInvocationSuccess, ev.eventName)
	assert.Equal(t, "run-1", ev.data.GetByKey("runId").StringValue())
	assert.Equal(t, "my-graph", ev.data.GetByKey("graphKey").StringValue())
	assert.Equal(t, "var-abc", ev.data.GetByKey("variationKey").StringValue())
	assert.Equal(t, 2, ev.data.GetByKey("version").IntValue())
}

func TestGraphTracker_ResumptionToken_OmitsEmptyVariationKey(t *testing.T) {
	events := newMockEvents()
	tracker := makeGraphTracker(events, "")
	raw, err := base64.RawURLEncoding.DecodeString(tracker.ResumptionToken())
	require.NoError(t, err)

	var decoded map[string]interface{}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	_, hasVariation := decoded["variationKey"]
	assert.False(t, hasVariation)
	assert.Equal(t, "run-1", decoded["runId"])
	assert.Equal(t, "my-graph", decoded["graphKey"])
	assert.Equal(t, float64(2), decoded["version"])
}

func TestGraphTracker_CreateGraphTracker_InvalidToken(t *testing.T) {
	sdk := newMultiFlagMockSDK(nil)
	client, err := NewClient(sdk)
	require.NoError(t, err)

	t.Run("malformed base64", func(t *testing.T) {
		_, err := client.CreateGraphTracker("not-valid-base64!!!", ldcontext.New("user"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid graph resumption token")
	})

	t.Run("missing required fields", func(t *testing.T) {
		token := base64.RawURLEncoding.EncodeToString([]byte(`{"version":1}`))
		_, err := client.CreateGraphTracker(token, ldcontext.New("user"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required fields")
	})

	t.Run("empty graphKey", func(t *testing.T) {
		token := base64.RawURLEncoding.EncodeToString([]byte(`{"runId":"r1","graphKey":"","version":1}`))
		_, err := client.CreateGraphTracker(token, ldcontext.New("user"))
		require.Error(t, err)
	})
}

func TestGraphTracker_GetSummary_IncludesResumptionToken(t *testing.T) {
	events := newMockEvents()
	tracker := makeGraphTracker(events, "v")
	summary := tracker.GetSummary()
	assert.Equal(t, tracker.ResumptionToken(), summary.ResumptionToken)
	assert.True(t, summary.Success.IsNone())
	assert.Nil(t, summary.Path)
}

func TestGraphTracker_TrackDuration_AtMostOnceUnderContention(t *testing.T) {
	events := newMockEvents()
	tracker := makeGraphTracker(events, "")

	runGraphTrackerContention(graphTrackerContentionGoroutines, func() {
		_ = tracker.TrackDuration(42)
	})

	assert.Equal(t, 1, countEventsNamed(events, graphDurationTotal))
	require.True(t, tracker.GetSummary().DurationMs.IsSome())
	assert.Equal(t, 42.0, tracker.GetSummary().DurationMs.Unwrap())
}

func TestGraphTracker_TrackTotalTokens_AtMostOnceUnderContention(t *testing.T) {
	events := newMockEvents()
	tracker := makeGraphTracker(events, "")
	usage := TokenUsage{Total: 10, Input: 6, Output: 4}

	runGraphTrackerContention(graphTrackerContentionGoroutines, func() {
		_ = tracker.TrackTotalTokens(usage)
	})

	assert.Equal(t, 1, countEventsNamed(events, graphTotalTokens))
	require.True(t, tracker.GetSummary().Tokens.IsSome())
	assert.Equal(t, 10, tracker.GetSummary().Tokens.Unwrap().Total)
}

func TestGraphTracker_TrackPath_AtMostOnceUnderContention(t *testing.T) {
	events := newMockEvents()
	tracker := makeGraphTracker(events, "")
	path := []string{"a", "b"}

	runGraphTrackerContention(graphTrackerContentionGoroutines, func() {
		_ = tracker.TrackPath(path)
	})

	assert.Equal(t, 1, countEventsNamed(events, graphPath))
	assert.Equal(t, []string{"a", "b"}, tracker.GetSummary().Path)
}

func TestGraphTracker_TrackInvocationSuccess_AtMostOnceUnderContention(t *testing.T) {
	events := newMockEvents()
	tracker := makeGraphTracker(events, "")

	runGraphTrackerContention(graphTrackerContentionGoroutines, func() {
		_ = tracker.TrackInvocationSuccess()
	})

	assert.Equal(t, 1, countEventsNamed(events, graphInvocationSuccess))
	assert.Equal(t, 0, countEventsNamed(events, graphInvocationFailure))
	require.True(t, tracker.GetSummary().Success.IsSome())
	assert.True(t, tracker.GetSummary().Success.Unwrap())
}

func TestGraphTracker_TrackInvocation_MutualExclusionUnderContention(t *testing.T) {
	events := newMockEvents()
	tracker := makeGraphTracker(events, "")

	var ready, done sync.WaitGroup
	start := make(chan struct{})
	n := graphTrackerContentionGoroutines
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		i := i
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			if i%2 == 0 {
				_ = tracker.TrackInvocationSuccess()
			} else {
				_ = tracker.TrackInvocationFailure()
			}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()

	successEvents := countEventsNamed(events, graphInvocationSuccess)
	failureEvents := countEventsNamed(events, graphInvocationFailure)
	assert.Equal(t, 1, successEvents+failureEvents, "exactly one invocation event should fire")
	require.True(t, tracker.GetSummary().Success.IsSome())
	if successEvents == 1 {
		assert.True(t, tracker.GetSummary().Success.Unwrap())
		assert.Equal(t, 0, failureEvents)
	} else {
		assert.False(t, tracker.GetSummary().Success.Unwrap())
		assert.Equal(t, 1, failureEvents)
	}
}
