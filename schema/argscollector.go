package schema

import (
	"database/sql"
	"reflect"
	"time"
)

// PlaceholderAppender is an optional interface implemented by dialects whose bind
// placeholder syntax differs from the default `?`, for example `$1` (PostgreSQL)
// or `@p1` (MSSQL). idx is the 1-based position of the argument.
type PlaceholderAppender interface {
	AppendPlaceholder(b []byte, idx int) []byte
}

// AppendPlaceholder appends a bind placeholder for the idx-th (1-based) argument
// using the syntax of the given dialect.
func AppendPlaceholder(d Dialect, b []byte, idx int) []byte {
	if d, ok := d.(PlaceholderAppender); ok {
		return d.AppendPlaceholder(b, idx)
	}
	return append(b, '?')
}

// PlaceholderStyle selects the bind placeholder syntax used when a query is
// generated with an ArgsCollector.
type PlaceholderStyle int

const (
	// DialectPlaceholder uses the native syntax of the dialect: `?` for MySQL and
	// SQLite, `$1` for PostgreSQL, `@p1` for MSSQL. This is what database/sql
	// drivers expect, so use it for prepared statements.
	DialectPlaceholder PlaceholderStyle = iota

	// QuestionPlaceholder always uses `?`, regardless of the dialect.
	QuestionPlaceholder
)

// ArgsCollector collects query arguments while a query is generated with bind
// placeholders instead of inlined values. See QueryGen.WithArgsCollector.
//
// The zero value is ready to use. ArgsCollector is not safe for concurrent use.
type ArgsCollector struct {
	// Style selects the bind placeholder syntax. The zero value uses the syntax
	// of the dialect the query is generated for.
	Style PlaceholderStyle

	args []any
}

// NewArgsCollector returns an empty ArgsCollector using the given placeholder style.
func NewArgsCollector(style PlaceholderStyle) *ArgsCollector {
	return &ArgsCollector{Style: style}
}

// Args returns the collected arguments in the same order as the placeholders
// that were appended to the query.
func (c *ArgsCollector) Args() []any {
	return c.args
}

// Len returns the number of collected arguments.
func (c *ArgsCollector) Len() int {
	return len(c.args)
}

// add appends the value and returns its 1-based position.
func (c *ArgsCollector) add(v any) int {
	c.args = append(c.args, v)
	return len(c.args)
}

// BindableValue reports whether v can be passed to a database/sql driver as a
// query argument. Values that Bun renders in a dialect-specific way (JSON, arrays,
// msgpack, ...) are not bindable and keep being inlined into the query.
//
// Nullable wrappers such as sql.NullString, sql.NullTime or sql.Null[T] are
// bindable because they implement driver.Valuer, which database/sql resolves
// to NULL or to the underlying value.
func BindableValue(v any) bool {
	switch v.(type) {
	case nil,
		bool, *bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64,
		string, *string,
		[]byte,
		time.Time, *time.Time,
		sql.NullBool, sql.NullByte, sql.NullFloat64, sql.NullInt16,
		sql.NullInt32, sql.NullInt64, sql.NullString, sql.NullTime:
		return true
	}
	return bindableType(reflect.TypeOf(v))
}

func bindableType(typ reflect.Type) bool {
	if typ == nil {
		return false
	}

	// Types with a custom SQL representation are inlined by Bun; sending them to
	// the driver instead would change (or break) the query.
	if typ.Implements(queryAppenderType) ||
		(typ.Kind() != reflect.Ptr && reflect.PointerTo(typ).Implements(queryAppenderType)) {
		return false
	}

	// database/sql converts driver.Valuer itself. This covers sql.NullXXX,
	// sql.Null[T], uuid.UUID and other nullable/custom types.
	if typ.Implements(driverValuerType) {
		return true
	}

	switch typ {
	case timeType, bytesType:
		return true
	case jsonRawMessageType:
		// Bun inlines raw JSON as a quoted string.
		return false
	}

	switch typ.Kind() {
	case reflect.Bool,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.String:
		return true
	case reflect.Ptr:
		return bindableType(typ.Elem())
	default:
		// Slices, arrays, maps and structs are marshaled by Bun (JSON, arrays, ...)
		// and must stay inlined.
		return false
	}
}
