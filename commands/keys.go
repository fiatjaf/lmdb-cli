package commands

import "lmdb-cli/core"

type Keys struct {
	PageSize int
}

func (cmd Keys) Execute(context *core.Context, input []byte) (err error) {
	args, err := parseRange(input, 0, 1)
	if err != nil {
		return err
	}

	var prefix []byte
	if len(args) == 1 {
		prefix = args[0]
	}

	if err := context.PrepareCursor(prefix, false); err != nil {
		return err
	}
	return Iterate{cmd.PageSize}.execute(context, true)
}
