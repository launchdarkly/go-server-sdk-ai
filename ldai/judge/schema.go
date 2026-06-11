package judge

// JSON Schema keywords used to build the judge's structured-output schema.
const (
	schemaType                 = "type"
	schemaProperties           = "properties"
	schemaDescription          = "description"
	schemaRequired             = "required"
	schemaAdditionalProperties = "additionalProperties"
	schemaObject               = "object"
)

func buildSchema(metricKey string) map[string]interface{} {
	if metricKey == "" {
		return map[string]interface{}{}
	}

	return map[string]interface{}{
		schemaType: schemaObject,
		schemaProperties: map[string]interface{}{
			"evaluations": map[string]interface{}{
				schemaType:        schemaObject,
				schemaDescription: "Object containing evaluation results for " + metricKey + " metric",
				schemaProperties: map[string]interface{}{
					metricKey: map[string]interface{}{
						schemaType: schemaObject,
						schemaProperties: map[string]interface{}{
							"score": map[string]interface{}{
								schemaType:        "number",
								"minimum":         0.0,
								"maximum":         1.0,
								schemaDescription: "Score between 0.0 and 1.0 for " + metricKey,
							},
							"reasoning": map[string]interface{}{
								schemaType:        "string",
								schemaDescription: "Reasoning behind the score for " + metricKey,
							},
						},
						schemaRequired:             []string{"score", "reasoning"},
						schemaAdditionalProperties: false,
					},
				},
				schemaRequired:             []string{metricKey},
				schemaAdditionalProperties: false,
			},
		},
		schemaRequired:             []string{"evaluations"},
		schemaAdditionalProperties: false,
	}
}
