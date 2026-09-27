package prompts

// Response schemas are JSON Schema documents built as fresh map[string]any
// values on every call, so callers may keep or mutate them freely. They use
// only the keywords type, properties, required, items, enum, description,
// minItems, maxItems, minimum, maximum, minLength, maxLength, and nullable,
// which every supported provider accepts.

// objectSchema describes an object with the given properties; required
// lists the property names that must be present.
func objectSchema(description string, properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{"type": "object", "properties": properties}
	if description != "" {
		schema["description"] = description
	}
	if len(required) > 0 {
		schema["required"] = append([]string{}, required...)
	}
	return schema
}

// stringSchema describes a string; maximumLength <= 0 leaves it unbounded.
func stringSchema(description string, maximumLength int) map[string]any {
	schema := map[string]any{"type": "string", "description": description}
	if maximumLength > 0 {
		schema["maxLength"] = maximumLength
	}
	return schema
}

// requiredStringSchema describes a non-empty string.
func requiredStringSchema(description string, maximumLength int) map[string]any {
	schema := stringSchema(description, maximumLength)
	schema["minLength"] = 1
	return schema
}

// enumStringSchema describes a string restricted to values. An empty value
// list degrades to an unrestricted string because an empty enum is invalid.
func enumStringSchema(description string, values []string) map[string]any {
	schema := map[string]any{"type": "string", "description": description}
	if len(values) > 0 {
		schema["enum"] = append([]string{}, values...)
	}
	return schema
}

// booleanSchema describes a boolean.
func booleanSchema(description string) map[string]any {
	return map[string]any{"type": "boolean", "description": description}
}

// integerSchema describes an integer in [minimum, maximum].
func integerSchema(description string, minimum int, maximum int) map[string]any {
	return map[string]any{"type": "integer", "description": description, "minimum": minimum, "maximum": maximum}
}

// arraySchema describes an array of items; bounds <= 0 are omitted.
func arraySchema(description string, items map[string]any, minimumItems int, maximumItems int) map[string]any {
	schema := map[string]any{"type": "array", "description": description, "items": items}
	if minimumItems > 0 {
		schema["minItems"] = minimumItems
	}
	if maximumItems > 0 {
		schema["maxItems"] = maximumItems
	}
	return schema
}

// sourceReferenceSchema describes one model.SourceReference.
func sourceReferenceSchema(urlDescription string) map[string]any {
	return objectSchema("A cited source.", map[string]any{
		"label": stringSchema("Short human-readable label for the source, such as the pull request title.", maximumReferenceLabelRunes),
		"url":   requiredStringSchema(urlDescription, 0),
	}, "label", "url")
}

// changeHighlightSchema describes one model.ChangeHighlight.
func changeHighlightSchema(urlDescription string) map[string]any {
	return objectSchema("One meaningful capability, feature, or technical improvement.", map[string]any{
		"title":        requiredStringSchema("Short, specific title of the change.", maximumHighlightTitleRunes),
		"whatChanged":  stringSchema("What was built or changed, concretely.", maximumHighlightFieldRunes),
		"howItWorks":   stringSchema("How it works: the mechanism, as far as the sources explain it.", maximumHighlightFieldRunes),
		"whyItMatters": stringSchema("Why it matters to users or developers.", maximumHighlightFieldRunes),
		"references": arraySchema("Sources this highlight is based on.",
			sourceReferenceSchema(urlDescription), 0, maximumReferencesPerHighlight),
	}, "title", "whatChanged", "howItWorks", "whyItMatters", "references")
}
