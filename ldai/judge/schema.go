package judge

// JSON Schema keywords used in the judge's structured-output schema.
const (
	schemaType        = "type"
	schemaDescription = "description"
)

// buildSchema returns the fixed structured-output schema for judge evaluations: a top-level object
// with required score (number, 0-1) and reasoning (string) properties. The judge config's
// evaluation metric key is not part of the schema; it is only used to key the tracked result.
func buildSchema() map[string]interface{} {
	return map[string]interface{}{
		"title":           "EvaluationResponse",
		schemaDescription: "Response containing an evaluation (score and reasoning).",
		schemaType:        "object",
		"properties": map[string]interface{}{
			"score": map[string]interface{}{
				schemaType:        "number",
				"minimum":         0.0,
				"maximum":         1.0,
				schemaDescription: "Score between 0.0 and 1.0.",
			},
			"reasoning": map[string]interface{}{
				schemaType:        "string",
				schemaDescription: "Reasoning behind the score.",
			},
		},
		"required":             []string{"score", "reasoning"},
		"additionalProperties": false,
	}
}
