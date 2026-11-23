package v2

// Field helper constructors for creating structured log fields
// These provide type-safe ways to create fields

// String creates a string field
func String(key, value string) Field {
	return Field{
		Key:   key,
		Value: value,
	}
}

// Int creates an integer field
func Int(key string, value int) Field {
	return Field{
		Key:   key,
		Value: value,
	}
}

// Error creates an error field
// This is a convenience method for logging errors as fields
func Error(err error) Field {
	return Field{
		Key:   "error",
		Value: err,
	}
}

// Any creates a field with any value type
// Use this for complex types or when the type is not known at compile time
func Any(key string, value interface{}) Field {
	return Field{
		Key:   key,
		Value: value,
	}
}
