package db

import (
	"context"
	"database/sql"
	"errors"
)

// InTx runs fn in one transaction, all of its writes land or none.
func (q *Queries) InTx(ctx context.Context, fn func(*Queries) error) error {
	conn, ok := q.db.(*sql.DB)
	if !ok {
		return errors.New("already inside a transaction")
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(q.WithTx(tx)); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	return tx.Commit()
}
