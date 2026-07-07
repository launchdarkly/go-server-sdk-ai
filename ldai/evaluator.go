package ldai

// Evaluator evaluates AI-generated responses against judge configs and tracks scores.
// This type will be fully implemented in Task 05; for now it acts as a non-nil stub
// so that the Evaluator() accessor on all typed configs never returns nil.
type Evaluator struct{}

// newNoopEvaluator returns a non-nil Evaluator that performs no evaluation.
func newNoopEvaluator() *Evaluator { return &Evaluator{} }
