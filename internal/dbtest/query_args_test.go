package dbtest_test

import (
	"database/sql"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/sqliteshim"
	"github.com/uptrace/bun/schema"
)

type ArgsUser struct {
	bun.BaseModel `bun:"table:users,alias:u"`

	ID        int64 `bun:",pk,autoincrement"`
	Name      string
	Email     sql.NullString
	Attrs     map[string]any `bun:",type:jsonb"`
	DeletedAt sql.NullTime
}

// newArgsDB returns a *bun.DB for the given dialect. The underlying *sql.DB is
// only needed because some dialects probe the server version on Init; no query
// is executed by these tests.
func newArgsDB(tb testing.TB, dialect schema.Dialect) *bun.DB {
	sqldb, err := sql.Open(sqliteshim.DriverName(), filepath.Join(tb.TempDir(), "args.db"))
	require.NoError(tb, err)
	tb.Cleanup(func() {
		require.NoError(tb, sqldb.Close())
	})
	return bun.NewDB(sqldb, dialect)
}

// wantPlaceholders returns the placeholders the dialect uses for the first n args.
func wantPlaceholders(dialectName string, n int) []string {
	ph := make([]string, n)
	for i := range ph {
		switch dialectName {
		case "pg":
			ph[i] = "$" + itoa(i+1)
		case "mssql":
			ph[i] = "@p" + itoa(i+1)
		default:
			ph[i] = "?"
		}
	}
	return ph
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

// ident quotes name the way the dialect does.
func ident(dialect schema.Dialect, name string) string {
	q := string(dialect.IdentQuote())
	return q + name + q
}

// TestSelectQueryWithArgsExec runs the generated query as a prepared statement
// against a real database to make sure the placeholder syntax and the args are
// what the driver expects.
func TestSelectQueryWithArgsExec(t *testing.T) {
	type ArgsRow struct {
		bun.BaseModel `bun:"table:args_rows,alias:r"`

		ID   int64 `bun:",pk,autoincrement"`
		Name string
		Num  int64
	}

	testEachDB(t, func(t *testing.T, dbName string, db *bun.DB) {
		mustResetModel(t, ctx, db, (*ArgsRow)(nil))

		_, err := db.NewInsert().Model(&[]ArgsRow{
			{Name: "one", Num: 1},
			{Name: "two", Num: 2},
		}).Exec(ctx)
		require.NoError(t, err)

		query, args, err := db.NewSelect().
			Model((*ArgsRow)(nil)).
			Column("name").
			Where("name = ?", "two").
			Where("num > ?", 1).
			QueryWithArgs()
		require.NoError(t, err)
		require.Equal(t, []any{"two", 1}, args)

		stmt, err := db.PrepareContext(ctx, query)
		require.NoError(t, err)
		defer stmt.Close()

		var name string
		require.NoError(t, stmt.QueryRowContext(ctx, args...).Scan(&name))
		require.Equal(t, "two", name)
	})
}

// TestSelectQueryWithArgsExecTypes checks that the collected args are accepted by
// the drivers for the types Bun binds instead of inlining.
func TestSelectQueryWithArgsExecTypes(t *testing.T) {
	type ArgsTypedRow struct {
		bun.BaseModel `bun:"table:args_typed_rows,alias:r"`

		ID      int64  `bun:",pk,autoincrement"`
		Name    string `bun:",notnull"`
		Num     int64
		Ratio   float64
		Flag    bool
		Data    []byte
		Created time.Time
		Note    sql.NullString
	}

	created := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	testEachDB(t, func(t *testing.T, dbName string, db *bun.DB) {
		mustResetModel(t, ctx, db, (*ArgsTypedRow)(nil))

		row := &ArgsTypedRow{
			Name:    "hello",
			Num:     42,
			Ratio:   1.5,
			Flag:    true,
			Data:    []byte("raw"),
			Created: created,
			Note:    sql.NullString{String: "note", Valid: true},
		}
		_, err := db.NewInsert().Model(row).Exec(ctx)
		require.NoError(t, err)

		query, args, err := db.NewSelect().
			Model((*ArgsTypedRow)(nil)).
			Column("name").
			Where("name = ?", "hello").
			Where("num = ?", int64(42)).
			Where("ratio = ?", 1.5).
			Where("flag = ?", true).
			Where("created <= ?", created.Add(time.Hour)).
			Where("note = ?", sql.NullString{String: "note", Valid: true}).
			QueryWithArgs()
		require.NoError(t, err)
		require.Len(t, args, 6)

		// NOTE: db.QueryRowContext is Bun's own wrapper - it re-formats the query
		// and does not pass args to the driver. Generated queries must be executed
		// on the underlying *sql.DB (or via a prepared statement).
		var name string
		err = db.DB.QueryRowContext(ctx, query, args...).Scan(&name)
		require.NoError(t, err)
		require.Equal(t, "hello", name)
	})
}

func TestSelectQueryWithArgs(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		q := db.NewSelect().
			Model((*ArgsUser)(nil)).
			Where("name = ?", "hello").
			Where("id > ?", 42)

		query, args, err := q.QueryWithArgs()
		require.NoError(t, err)

		ph := wantPlaceholders(dialectName, 2)
		require.Contains(t, query, "(name = "+ph[0]+") AND (id > "+ph[1]+")")
		require.Equal(t, []any{"hello", 42}, args)

		// Values are no longer inlined.
		require.NotContains(t, query, "'hello'")
		require.NotContains(t, query, "42")

		// The regular rendering is unchanged.
		require.Contains(t, q.String(), "'hello'")
		require.Contains(t, q.String(), "42")
	})
}

func TestSelectQueryWithArgsQuestionStyle(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		query, args, err := db.NewSelect().
			Model((*ArgsUser)(nil)).
			Where("name = ?", "hello").
			Where("id > ?", 42).
			QueryWithArgs(schema.QuestionPlaceholder)
		require.NoError(t, err)

		require.Contains(t, query, "(name = ?) AND (id > ?)")
		require.Equal(t, []any{"hello", 42}, args)
	})
}

func TestSelectQueryWithArgsBindableTypes(t *testing.T) {
	tm := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		query, args, err := db.NewSelect().
			Model((*ArgsUser)(nil)).
			Where("name = ?", sql.NullString{String: "hello", Valid: true}).
			Where("deleted_at > ?", sql.NullTime{Time: tm, Valid: true}).
			Where("created_at < ?", tm).
			Where("data = ?", []byte("raw")).
			Where("flag = ?", true).
			Where("score = ?", 1.5).
			Where("opt = ?", sql.Null[int64]{V: 3, Valid: true}).
			QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, []any{
			sql.NullString{String: "hello", Valid: true},
			sql.NullTime{Time: tm, Valid: true},
			tm,
			[]byte("raw"),
			true,
			1.5,
			sql.Null[int64]{V: 3, Valid: true},
		}, args)

		require.NotContains(t, query, "'hello'")
		require.NotContains(t, query, "'raw'")
	})
}

func TestSelectQueryWithArgsInlinedValues(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		sub := db.NewSelect().Model((*ArgsUser)(nil)).Column("id").Where("name = ?", "sub")

		query, args, err := db.NewSelect().
			Model((*ArgsUser)(nil)).
			Where("attrs = ?", map[string]any{"a": 1}).
			Where("id IN (?)", sub).
			Where("name = ?", bun.Ident("other")).
			QueryWithArgs()
		require.NoError(t, err)

		// JSON, identifiers and sub-queries cannot be bound and stay inlined,
		// but the args of the sub-query itself are bound.
		require.Equal(t, []any{"sub"}, args)
		require.Contains(t, query, `attrs = '{"a":1}'`)
		require.Contains(t, query, "name = "+string(dialect.IdentQuote())+"other"+string(dialect.IdentQuote()))
	})
}

func TestSelectQueryWithArgsPgArray(t *testing.T) {
	db := newArgsDB(t, pgdialect.New())

	query, args, err := db.NewSelect().
		Model((*ArgsUser)(nil)).
		Where("tags = ?", pgdialect.Array([]string{"a", "b"})).
		Where("name = ?", "hello").
		QueryWithArgs()
	require.NoError(t, err)

	require.Equal(t, []any{"hello"}, args)
	require.Contains(t, query, `tags = '{"a","b"}'`)
	require.Contains(t, query, "name = $1")
}

func TestSelectQueryWithArgsWherePK(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		model := &ArgsUser{ID: 7, Name: "hello"}
		query, args, err := db.NewSelect().Model(model).WherePK().QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, []any{int64(7)}, args)
		require.NotContains(t, query, "= 7")
		require.Contains(t, query, wantPlaceholders(dialectName, 1)[0])
	})
}

func TestSelectQueryWithArgsNamedArg(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		model := &ArgsUser{ID: 7, Name: "hello"}
		query, args, err := db.NewSelect().
			Model(model).
			Where("name = ?name").
			Where("id = ?TableAlias.id").
			QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, []any{"hello"}, args)
		require.Contains(t, query, "name = "+wantPlaceholders(dialectName, 1)[0])
		require.NotContains(t, query, "'hello'")
	})
}

func TestSelectQueryWithArgsRelation(t *testing.T) {
	type ArgsProfile struct {
		bun.BaseModel `bun:"table:profiles,alias:p"`

		ID     int64 `bun:",pk,autoincrement"`
		UserID int64
		Bio    string
	}

	type ArgsAuthor struct {
		bun.BaseModel `bun:"table:authors,alias:a"`

		ID      int64 `bun:",pk,autoincrement"`
		Name    string
		Profile *ArgsProfile `bun:"rel:has-one,join:id=user_id"`
	}

	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)
		db.RegisterModel((*ArgsProfile)(nil), (*ArgsAuthor)(nil))

		query, args, err := db.NewSelect().
			Model((*ArgsAuthor)(nil)).
			Relation("Profile", func(q *bun.SelectQuery) *bun.SelectQuery {
				return q.Where("bio != ?", "empty")
			}).
			Where("a.name = ?", "hello").
			QueryWithArgs()
		require.NoError(t, err)

		// The relation condition is merged into the WHERE clause after the base one.
		require.Equal(t, []any{"hello", "empty"}, args)
		ph := wantPlaceholders(dialectName, 2)
		require.Contains(t, query, "a.name = "+ph[0])
		require.Contains(t, query, "bio != "+ph[1])
	})
}

func TestSelectQueryWithArgsUnionAndCTE(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		cte := db.NewSelect().Model((*ArgsUser)(nil)).Where("id > ?", 1)
		other := db.NewSelect().Model((*ArgsUser)(nil)).Where("id < ?", 3)

		query, args, err := db.NewSelect().
			With("cte", cte).
			Model((*ArgsUser)(nil)).
			Where("name = ?", "hello").
			UnionAll(other).
			QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, []any{1, "hello", 3}, args)
		ph := wantPlaceholders(dialectName, 3)
		require.Contains(t, query, "id > "+ph[0])
		require.Contains(t, query, "name = "+ph[1])
		require.Contains(t, query, "id < "+ph[2])
	})
}

func TestSelectQueryWithArgsIsRepeatable(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		q := db.NewSelect().Model((*ArgsUser)(nil)).Where("name = ?", "hello")

		query1, args1, err := q.QueryWithArgs()
		require.NoError(t, err)
		query2, args2, err := q.QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, query1, query2)
		require.Equal(t, args1, args2)
		require.Len(t, args1, 1)
	})
}

//------------------------------------------------------------------------------

func TestInsertQueryWithArgs(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		user := &ArgsUser{Name: "hello", Email: sql.NullString{String: "a@b.c", Valid: true}}
		query, args, err := db.NewInsert().Model(user).QueryWithArgs()
		require.NoError(t, err)

		// name, email and deleted_at are bound; attrs is JSON and stays inlined.
		require.Equal(t, []any{
			"hello",
			sql.NullString{String: "a@b.c", Valid: true},
			sql.NullTime{},
		}, args)
		ph := wantPlaceholders(dialectName, 3)
		require.Contains(t, query, ph[0]+", "+ph[1]+", 'null', "+ph[2]+")")
		require.NotContains(t, query, "'hello'")
	})
}

func TestInsertQueryWithArgsBulk(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		users := []ArgsUser{{Name: "one"}, {Name: "two"}}
		query, args, err := db.NewInsert().Model(&users).QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, []any{
			"one", sql.NullString{}, sql.NullTime{},
			"two", sql.NullString{}, sql.NullTime{},
		}, args)
		require.NotContains(t, query, "'one'")
		require.NotContains(t, query, "'two'")
	})
}

func TestInsertQueryWithArgsMapModel(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		values := map[string]any{"name": "hello", "num": 42}
		query, args, err := db.NewInsert().Model(&values).Table("users").QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, []any{"hello", 42}, args)
		ph := wantPlaceholders(dialectName, 2)
		require.Contains(t, query, "VALUES ("+ph[0]+", "+ph[1]+")")
	})
}

func TestInsertQueryWithArgsValues(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		user := &ArgsUser{Name: "hello"}
		query, args, err := db.NewInsert().
			Model(user).
			Value("name", "?", "override").
			Value("email", "upper(?)", "a@b.c").
			QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, []any{"override", "a@b.c", sql.NullTime{}}, args)
		require.NotContains(t, query, "'override'")
		require.Contains(t, query, "upper("+wantPlaceholders(dialectName, 2)[1]+")")
	})
}

func TestUpdateQueryWithArgs(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		user := &ArgsUser{ID: 7, Name: "hello"}
		query, args, err := db.NewUpdate().
			Model(user).
			Column("name").
			WherePK().
			QueryWithArgs()
		require.NoError(t, err)

		// SET values are bound before WHERE values.
		require.Equal(t, []any{"hello", int64(7)}, args)
		ph := wantPlaceholders(dialectName, 2)
		require.Contains(t, query, "SET "+ident(dialect, "name")+" = "+ph[0])
		require.Contains(t, query, ident(dialect, "id")+" = "+ph[1])
	})
}

func TestUpdateQueryWithArgsSetExpr(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		query, args, err := db.NewUpdate().
			Model((*ArgsUser)(nil)).
			Set("name = ?", "hello").
			Set("email = ?", sql.NullString{}).
			Where("id = ?", 7).
			QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, []any{"hello", sql.NullString{}, 7}, args)
		ph := wantPlaceholders(dialectName, 3)
		require.Contains(t, query, "name = "+ph[0])
		require.Contains(t, query, "email = "+ph[1])
		require.Contains(t, query, "id = "+ph[2])
	})
}

func TestUpdateQueryWithArgsMapModel(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		values := map[string]any{"name": "hello"}
		query, args, err := db.NewUpdate().
			Model(&values).
			Table("users").
			Where("id = ?", 7).
			QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, []any{"hello", 7}, args)
		ph := wantPlaceholders(dialectName, 2)
		require.Contains(t, query, "SET "+ident(dialect, "name")+" = "+ph[0])
		require.Contains(t, query, "id = "+ph[1])
	})
}

func TestDeleteQueryWithArgs(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		query, args, err := db.NewDelete().
			Model((*ArgsUser)(nil)).
			Where("name = ?", "hello").
			Where("id > ?", 42).
			QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, []any{"hello", 42}, args)
		ph := wantPlaceholders(dialectName, 2)
		require.Contains(t, query, "name = "+ph[0])
		require.Contains(t, query, "id > "+ph[1])
	})
}

func TestDeleteQueryWithArgsWherePK(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		user := &ArgsUser{ID: 7}
		query, args, err := db.NewDelete().Model(user).WherePK().QueryWithArgs()
		require.NoError(t, err)

		require.Equal(t, []any{int64(7)}, args)
		require.NotContains(t, query, "= 7")
	})
}

func TestWriteQueriesWithArgsQuestionStyle(t *testing.T) {
	testEachDialect(t, func(t *testing.T, dialectName string, dialect schema.Dialect) {
		db := newArgsDB(t, dialect)

		user := &ArgsUser{ID: 7, Name: "hello"}

		query, args, err := db.NewInsert().Model(user).QueryWithArgs(schema.QuestionPlaceholder)
		require.NoError(t, err)
		require.Contains(t, query, "?")
		require.NotContains(t, query, "'hello'")
		require.NotEmpty(t, args)

		query, args, err = db.NewUpdate().Model(user).Column("name").WherePK().
			QueryWithArgs(schema.QuestionPlaceholder)
		require.NoError(t, err)
		require.Contains(t, query, "SET "+ident(dialect, "name")+" = ?")
		require.Len(t, args, 2)

		query, args, err = db.NewDelete().Model(user).WherePK().
			QueryWithArgs(schema.QuestionPlaceholder)
		require.NoError(t, err)
		require.Contains(t, query, "?")
		require.Len(t, args, 1)
	})
}

// TestWriteQueriesWithArgsExec runs the generated INSERT/UPDATE/DELETE queries
// against a real database.
func TestWriteQueriesWithArgsExec(t *testing.T) {
	type ArgsWriteRow struct {
		bun.BaseModel `bun:"table:args_write_rows,alias:w"`

		ID   int64 `bun:",pk,autoincrement"`
		Name string
		Num  int64
	}

	testEachDB(t, func(t *testing.T, dbName string, db *bun.DB) {
		mustResetModel(t, ctx, db, (*ArgsWriteRow)(nil))

		// INSERT
		query, args, err := db.NewInsert().
			Model(&ArgsWriteRow{Name: "hello", Num: 1}).
			QueryWithArgs()
		require.NoError(t, err)
		_, err = db.DB.ExecContext(ctx, query, args...)
		require.NoError(t, err)

		// UPDATE
		query, args, err = db.NewUpdate().
			Model((*ArgsWriteRow)(nil)).
			Set("num = ?", int64(2)).
			Where("name = ?", "hello").
			QueryWithArgs()
		require.NoError(t, err)
		res, err := db.DB.ExecContext(ctx, query, args...)
		require.NoError(t, err)
		n, err := res.RowsAffected()
		require.NoError(t, err)
		require.Equal(t, int64(1), n)

		var num int64
		require.NoError(t, db.NewSelect().
			Model((*ArgsWriteRow)(nil)).
			Column("num").
			Where("name = ?", "hello").
			Scan(ctx, &num))
		require.Equal(t, int64(2), num)

		// DELETE
		query, args, err = db.NewDelete().
			Model((*ArgsWriteRow)(nil)).
			Where("num = ?", int64(2)).
			QueryWithArgs()
		require.NoError(t, err)
		res, err = db.DB.ExecContext(ctx, query, args...)
		require.NoError(t, err)
		n, err = res.RowsAffected()
		require.NoError(t, err)
		require.Equal(t, int64(1), n)

		count, err := db.NewSelect().Model((*ArgsWriteRow)(nil)).Count(ctx)
		require.NoError(t, err)
		require.Equal(t, 0, count)
	})
}
