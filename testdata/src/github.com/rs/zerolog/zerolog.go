// Package zerolog is a stub implementation of github.com/rs/zerolog for testing
package zerolog

// Event represents a zerolog event
type Event struct{}

// Ctx adds context to the event
func (e *Event) Ctx(ctx interface{}) *Event {
	return e
}

// Str adds a string field to the event
func (e *Event) Str(key string, value string) *Event {
	return e
}

// Int adds an int field to the event
func (e *Event) Int(key string, value int) *Event {
	return e
}

// Bool adds a boolean field to the event
func (e *Event) Bool(key string, value bool) *Event {
	return e
}

// Err adds an error field to the event
func (e *Event) Err(err error) *Event {
	return e
}

// Timestamp adds a timestamp to the event
func (e *Event) Timestamp() *Event {
	return e
}

// Dur adds a duration field to the event
func (e *Event) Dur(key string, value interface{}) *Event {
	return e
}

// Msg sends the event with a message
func (e *Event) Msg(msg string) {
	// Terminal method that outputs a log message
}

// Msgf sends the event with a formatted message
func (e *Event) Msgf(format string, v ...interface{}) {
	// Terminal method that outputs a formatted log message
}

// Send sends the event
func (e *Event) Send() {
	// Terminal method that outputs a log message without text
}

// New creates a new logger
func New(w interface{}) Logger {
	return Logger{}
}

// NewConsoleWriter creates a new console writer
func NewConsoleWriter() interface{} {
	return nil
}

// Level represents a zerolog log level
type Level int8

// Logger represents a zerolog logger
type Logger struct{}

// Info creates an info level event
func (l Logger) Info() *Event {
	return &Event{}
}

// Error creates an error level event
func (l Logger) Error() *Event {
	return &Event{}
}

// Debug creates a debug level event
func (l Logger) Debug() *Event {
	return &Event{}
}

// Warn creates a warn level event
func (l Logger) Warn() *Event {
	return &Event{}
}

// Fatal creates a fatal level event
func (l Logger) Fatal() *Event {
	return &Event{}
}

// Panic creates a panic level event
func (l Logger) Panic() *Event {
	return &Event{}
}

// Log creates a log level event
func (l Logger) Log() *Event {
	return &Event{}
}

// Print creates a print level event
func (l Logger) Print() *Event {
	return &Event{}
}

// Printf creates a printf level event
func (l Logger) Printf(format string, v ...interface{}) {
	// No-op for testing
}

// Trace creates a trace level event
func (l Logger) Trace() *Event {
	return &Event{}
}

// With returns a new logger with the given context
func (l Logger) With() *Context {
	return &Context{}
}

// Level sets the logger level
func (l Logger) Level(level Level) Logger {
	return l
}

// Output sets the logger output
func (l Logger) Output(w interface{}) Logger {
	return l
}

// Context represents a zerolog context
type Context struct{}

// Logger returns a logger from the context
func (c *Context) Logger() Logger {
	return Logger{}
}

// Ctx adds context to the Context
func (c *Context) Ctx(ctx interface{}) *Context {
	return c
}

// Str adds a string field to the context
func (c *Context) Str(key string, value string) *Context {
	return c
}
