package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shridarpatil/whatomate/internal/contactquery"
	"github.com/shridarpatil/whatomate/internal/crmactions"
)

// Flow-control steps. Everything else in a rule's step list is an action from
// the shared CRM action library; these two belong to automations alone, which
// is why they live here rather than in crmactions: a chatbot node and a
// keyword rule have no path to split and no durable place to wait.
const (
	// StepCondition asks a question about the contact and takes one of two
	// paths. "If they are a customer, create a follow-up; otherwise tell
	// their owner" is the rule people describe out loud, and a straight list
	// could only express it as two rules that had to be kept in step.
	StepCondition = "condition"
	// StepWait pauses before the steps after it. "Wait a day, then check in"
	// is the other half of how people describe a process.
	StepWait = "wait"
)

// Limits on a rule's shape.
const (
	// MaxStepsPerRule counts every step, on every path.
	MaxStepsPerRule = 40
	// MaxNesting bounds how many questions deep a path may go. Past three,
	// a rule stops being something a person can read at a glance.
	MaxNesting = 3
	// MaxWait is the longest single pause. Run history is kept for thirty
	// days, and a customer waiting longer than that at one step is a process
	// better served by a date-field trigger.
	MaxWait = 30 * 24 * time.Hour
	// MinWait is the shortest pause the scheduler can honour. The resume job
	// runs every minute.
	MinWait = time.Minute
)

// FlowSteps lists the flow-control step types, for the catalog.
func FlowSteps() []string { return []string{StepCondition, StepWait} }

// IsFlowStep reports whether a step type is flow control rather than an
// action.
func IsFlowStep(stepType string) bool {
	return stepType == StepCondition || stepType == StepWait
}

// StepProblem is one reason a rule cannot run yet, pinned to the step that
// has it so the builder can mark that card rather than print a paragraph.
type StepProblem struct {
	StepID  string `json:"step_id,omitempty"`
	Message string `json:"message"`
}

// StepError is a validation failure that names its step.
type StepError struct {
	StepProblem
}

func (e *StepError) Error() string {
	if e.StepID == "" {
		return "automation: " + e.Message
	}
	return fmt.Sprintf("automation: step %s: %s", e.StepID, e.Message)
}

// conditionFilter reads a condition step's question.
func conditionFilter(cfg crmactions.Config) (contactquery.Filter, bool) {
	raw, err := json.Marshal(cfg["filter"])
	if err != nil {
		return contactquery.Filter{}, false
	}
	var filter contactquery.Filter
	if err := json.Unmarshal(raw, &filter); err != nil || filter.IsEmpty() {
		return contactquery.Filter{}, false
	}
	return filter, true
}

// waitDuration reads a wait step's pause.
func waitDuration(cfg crmactions.Config) (time.Duration, bool) {
	return cfg.Duration("for")
}

// stepChecker walks a step tree once, collecting what is structurally wrong
// (always an error) and what is merely unfinished (an error only when the rule
// is to run).
type stepChecker struct {
	ctx      context.Context
	registry func() (*contactquery.Registry, error)

	seen       map[string]bool
	count      int
	structural error
	problems   []StepProblem
}

func (c *stepChecker) walk(steps []ActionSpec, depth int) {
	for _, step := range steps {
		if c.structural != nil {
			return
		}
		c.count++
		if c.count > MaxStepsPerRule {
			c.structural = &StepError{StepProblem{Message: fmt.Sprintf("a rule may have at most %d steps", MaxStepsPerRule)}}
			return
		}
		if step.ID == "" {
			c.structural = &StepError{StepProblem{Message: "every step needs an id"}}
			return
		}
		if c.seen[step.ID] {
			c.structural = &StepError{StepProblem{StepID: step.ID, Message: "two steps share this id"}}
			return
		}
		c.seen[step.ID] = true

		switch step.Type {
		case StepCondition:
			if depth >= MaxNesting {
				c.structural = &StepError{StepProblem{StepID: step.ID,
					Message: fmt.Sprintf("questions can be nested at most %d deep", MaxNesting)}}
				return
			}
			c.checkCondition(step)
			c.walk(step.Then, depth+1)
			c.walk(step.Else, depth+1)
		case StepWait:
			if len(step.Then) > 0 || len(step.Else) > 0 {
				c.structural = &StepError{StepProblem{StepID: step.ID, Message: "only a question can split the path"}}
				return
			}
			c.checkWait(step)
		default:
			if _, known := crmactions.Lookup(step.Type); !known {
				c.structural = &StepError{StepProblem{StepID: step.ID,
					Message: fmt.Sprintf("%q is not a step this product can run", step.Type)}}
				return
			}
			if len(step.Then) > 0 || len(step.Else) > 0 {
				c.structural = &StepError{StepProblem{StepID: step.ID, Message: "only a question can split the path"}}
				return
			}
			if err := crmactions.Validate(step.Type, step.Config); err != nil {
				c.problems = append(c.problems, StepProblem{StepID: step.ID, Message: plainError(err)})
			}
		}
	}
}

func (c *stepChecker) checkCondition(step ActionSpec) {
	filter, ok := conditionFilter(step.Config)
	if !ok {
		c.problems = append(c.problems, StepProblem{StepID: step.ID, Message: "choose what to check about the contact"})
		return
	}
	registry, err := c.registry()
	if err != nil {
		c.structural = err
		return
	}
	if err := contactquery.Validate(registry, filter); err != nil {
		c.problems = append(c.problems, StepProblem{StepID: step.ID, Message: plainError(err)})
	}
}

func (c *stepChecker) checkWait(step ActionSpec) {
	wait, ok := waitDuration(step.Config)
	switch {
	case !ok:
		c.problems = append(c.problems, StepProblem{StepID: step.ID, Message: "choose how long to wait"})
	case wait < MinWait:
		c.problems = append(c.problems, StepProblem{StepID: step.ID, Message: "a wait must be at least a minute"})
	case wait > MaxWait:
		c.problems = append(c.problems, StepProblem{StepID: step.ID, Message: "a wait can be at most 30 days"})
	}
}

// plainError turns a validation error into a sentence a person can act on,
// dropping the package prefix that only means something to a developer.
func plainError(err error) string {
	msg := err.Error()
	for _, prefix := range []string{"automation: ", "crmactions: ", "contactquery: "} {
		msg = strings.TrimPrefix(msg, prefix)
	}
	return msg
}

// countSteps counts every step on every path.
func countSteps(steps []ActionSpec) int {
	n := 0
	for _, step := range steps {
		n += 1 + countSteps(step.Then) + countSteps(step.Else)
	}
	return n
}

// continuationAfter finds a step by id and returns what runs after it: the
// rest of its own list, then the rest of each enclosing list on the way back
// out. It is how a paused run resumes, and it reads the rule as it is now —
// an edit made while somebody waits applies to the steps they have not
// reached.
func continuationAfter(steps []ActionSpec, stepID string) ([][]ActionSpec, bool) {
	for i, step := range steps {
		if step.ID == stepID {
			return [][]ActionSpec{steps[i+1:]}, true
		}
		for _, branch := range [][]ActionSpec{step.Then, step.Else} {
			if inner, found := continuationAfter(branch, stepID); found {
				return append(inner, steps[i+1:]), true
			}
		}
	}
	return nil, false
}

// findStep returns a step by id anywhere in the tree.
func findStep(steps []ActionSpec, stepID string) (ActionSpec, bool) {
	for _, step := range steps {
		if step.ID == stepID {
			return step, true
		}
		for _, branch := range [][]ActionSpec{step.Then, step.Else} {
			if found, ok := findStep(branch, stepID); ok {
				return found, true
			}
		}
	}
	return ActionSpec{}, false
}
