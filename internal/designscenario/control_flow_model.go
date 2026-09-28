package designscenario

type ExecutionCondition struct {
	Variable string  `json:"variable"`
	Operator string  `json:"operator"`
	Value    *string `json:"value,omitempty"`
}
type FragmentExecution struct {
	Condition  *ExecutionCondition `json:"condition,omitempty"`
	Iterations int                 `json:"iterations,omitzero"`
}
type BranchExecution struct {
	Condition *ExecutionCondition `json:"condition,omitempty"`
	Otherwise bool                `json:"otherwise,omitzero"`
}
type LoopIteration struct {
	FragmentID string `json:"fragmentId"`
	Iteration  int    `json:"iteration"`
}
type ControlFlowResult struct {
	FragmentID string          `json:"fragmentId"`
	BranchID   string          `json:"branchId,omitempty"`
	Outcome    string          `json:"outcome"`
	Iterations []LoopIteration `json:"iterations,omitempty"`
	Reason     string          `json:"reason,omitempty"`
}

func (c *ExecutionCondition) UnmarshalJSON(data []byte) error {
	type wire ExecutionCondition
	var out wire
	if err := decodeExecutionObject(data, &out, "variable", "operator"); err != nil {
		return err
	}
	*c = ExecutionCondition(out)
	return nil
}
func (e *FragmentExecution) UnmarshalJSON(data []byte) error {
	type wire FragmentExecution
	var out wire
	if err := decodeExecutionObject(data, &out); err != nil {
		return err
	}
	*e = FragmentExecution(out)
	return nil
}
func (e *BranchExecution) UnmarshalJSON(data []byte) error {
	type wire BranchExecution
	var out wire
	if err := decodeExecutionObject(data, &out); err != nil {
		return err
	}
	*e = BranchExecution(out)
	return nil
}
