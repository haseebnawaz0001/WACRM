package calling

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shridarpatil/whatomate/internal/flowgraph"
	"github.com/stretchr/testify/assert"
	"github.com/zerodha/logf"
)

// stubMatcher answers a fixed way, so the node's own decisions are what is
// being tested rather than the filter compiler's.
type stubMatcher struct {
	matched bool
	err     error
	calls   int
}

func (s *stubMatcher) Matches(_ context.Context, _, _ uuid.UUID, _ map[string]any) (bool, error) {
	s.calls++
	return s.matched, s.err
}

func conditionNode(filter map[string]any) *IVRNode {
	return &flowgraph.Node[IVRNodeType]{
		ID:     "n1",
		Type:   IVRNodeCRMCondition,
		Config: map[string]any{"filter": filter},
	}
}

func conditionSession() *CallSession {
	return &CallSession{
		ID:             "call-1",
		OrganizationID: uuid.New(),
		ContactID:      uuid.New(),
	}
}

func managerWith(matcher ContactMatcher) *Manager {
	return &Manager{log: logf.New(logf.Opts{}), matcher: matcher}
}

func TestExecuteCRMCondition_TakesTheMatchEdge(t *testing.T) {
	m := managerWith(&stubMatcher{matched: true})
	outcome := m.executeCRMCondition(conditionSession(),
		conditionNode(map[string]any{"op": "and", "children": []any{"x"}}))
	assert.Equal(t, "match", outcome)
}

func TestExecuteCRMCondition_TakesTheNoMatchEdge(t *testing.T) {
	m := managerWith(&stubMatcher{matched: false})
	outcome := m.executeCRMCondition(conditionSession(),
		conditionNode(map[string]any{"op": "and", "children": []any{"x"}}))
	assert.Equal(t, "no_match", outcome)
}

// A call is in progress. A failed lookup has to keep it moving, and it must not
// route a stranger to the account manager.
func TestExecuteCRMCondition_AFailedLookupIsNotAMatch(t *testing.T) {
	m := managerWith(&stubMatcher{err: errors.New("database is away")})
	outcome := m.executeCRMCondition(conditionSession(),
		conditionNode(map[string]any{"op": "and", "children": []any{"x"}}))
	assert.Equal(t, "no_match", outcome)
}

// An empty condition matches nobody rather than everybody: a node somebody
// dropped on the canvas and never configured must not silently route every
// caller down the VIP path.
func TestExecuteCRMCondition_AnEmptyFilterMatchesNobody(t *testing.T) {
	matcher := &stubMatcher{matched: true}
	m := managerWith(matcher)
	outcome := m.executeCRMCondition(conditionSession(), conditionNode(nil))
	assert.Equal(t, "no_match", outcome)
	assert.Zero(t, matcher.calls, "an unconfigured node should not query at all")
}

// An unknown caller — a number that resolved to no contact — is not a match.
func TestExecuteCRMCondition_AnUnknownCallerIsNotAMatch(t *testing.T) {
	matcher := &stubMatcher{matched: true}
	m := managerWith(matcher)
	session := conditionSession()
	session.ContactID = uuid.Nil

	outcome := m.executeCRMCondition(session,
		conditionNode(map[string]any{"op": "and", "children": []any{"x"}}))
	assert.Equal(t, "no_match", outcome)
	assert.Zero(t, matcher.calls)
}

// Without a matcher wired in, the node still has to answer.
func TestExecuteCRMCondition_WithoutAMatcherItDoesNotPanic(t *testing.T) {
	m := managerWith(nil)
	outcome := m.executeCRMCondition(conditionSession(),
		conditionNode(map[string]any{"op": "and", "children": []any{"x"}}))
	assert.Equal(t, "no_match", outcome)
}
