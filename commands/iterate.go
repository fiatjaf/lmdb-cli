package commands

import (
	"bytes"
	"lmdb-cli/core"

	"github.com/PowerDNS/lmdb-go/lmdb"
)

type Iterate struct {
	PageSize int
}

func (cmd Iterate) Execute(context *core.Context, input []byte) (err error) {
	return cmd.execute(context, false)
}

func (cmd Iterate) execute(context *core.Context, first bool) (err error) {
	cursor := context.Cursor
	if cursor == nil {
		return nil
	}
	for i := 0; i < cmd.PageSize; i++ {
		var err error
		var key, value []byte
		if first && cursor.Prefix != nil {
			key, value, err = cursor.Get(cursor.Prefix, nil, lmdb.SetRange)
		} else {
			key, value, err = cursor.Get(nil, nil, lmdb.Next)
		}
		first = false

		if lmdb.IsNotFound(err) || (cursor.Prefix != nil && !bytes.HasPrefix(key, cursor.Prefix)) {
			context.CloseCursor()
			return nil
		}
		if err != nil {
			context.CloseCursor()
			return err
		}
		context.Output(key)
		if cursor.IncludeValues {
			context.Output(value)
			context.Output(nil)
		}
	}
	context.Output(SCAN_MORE)
	return nil
}
