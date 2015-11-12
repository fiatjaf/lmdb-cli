// a command line interface to lmdb
package lmdbcli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"os"
	"path"
	"unicode"

	"github.com/szferi/gomdb"
)

var (
	pathFlag     = flag.String("db", "", "Relative path to lmdb file")
	sizeFlag     = flag.Float64("size", 2, "factor to allocate for growth or shrinkage")
	roFlag       = flag.Bool("ro", false, "open the database in read-only mode")
	requiredArgs = map[string]int{"scan": 0, "stat": 0, "expand": 0, "exists": 1, "get": 1, "del": 1, "put": 2, "exit": 0, "quit": 0, "it": 0}

	OK        = []byte("OK")
	SCAN_MORE = []byte(`"it" for more`)
)

const (
	STATE_NONE int = iota
	STATE_WORD
	STATE_QUOTE
	STATE_ESCAPED
)

type Command struct {
	fn        string
	key       []byte
	val       []byte
	jsonPrint bool
}

// Run golmdb using the directory containing the data as dbPath

func Run() {
	flag.Parse()

	if len(*pathFlag) == 0 && len(flag.Args()) == 1 {
		pathFlag = &flag.Args()[0]
	}
	if len(*pathFlag) == 0 {
		log.Fatal("-db must be specified")
	}

	size := uint64(1024 * 1024 * 32)
	if stat, err := os.Stat(path.Join(*pathFlag, "data.mdb")); err != nil {
		if os.IsNotExist(err) == false {
			log.Fatal("failed to stat data.mdb file: ", err)
		}
	} else {
		size = uint64(float64(stat.Size()) * *sizeFlag)
	}

	context := NewContext(*pathFlag, size, os.Stdout)
	defer context.Close()
	if err := context.SwitchDB(nil); err != nil {
		log.Fatal("could not select default database: ", err)
	}
	runShell(context, os.Stdin)
}

func runShell(context *Context, in io.Reader) {
	var err error
	reader := bufio.NewReader(in)
	for {
		context.Prompt()
		input, _ := reader.ReadSlice('\n')
		cmd, cerr := getCommand(parseInput(input))

		if cmd.fn != "it" && context.cursor != nil {
			context.CloseCursor()
		}

		if cerr != nil {
			context.Output([]byte(cerr.Error()))
		} else if cmd.fn == "get" {
			err = get(context, cmd.key, cmd.jsonPrint)
		} else if cmd.fn == "exists" {
			err = exists(context, cmd.key)
		} else if cmd.fn == "del" {
			err = del(context, cmd.key)
		} else if cmd.fn == "put" {
			err = put(context, cmd.key, cmd.val)
		} else if cmd.fn == "scan" {
			err = scan(context, cmd.key)
		} else if cmd.fn == "it" {
			err = iterate(context, false)
		} else if cmd.fn == "quit" || cmd.fn == "exit" {
			return
		}
		if err != nil {
			context.Output([]byte(err.Error()))
		}
	}
}

func get(context *Context, key []byte, jsonPrint bool) error {
	return context.WithinRead(func(txn *mdb.Txn) error {
		data, err := txn.Get(context.dbi, key)
		if err != nil {
			return err
		}
		if jsonPrint {
			var prettyData bytes.Buffer
			if err := json.Indent(&prettyData, data, "", "    "); err != nil {
				return err
			}
			context.Output(prettyData.Bytes())
		} else {
			context.Output(data)
		}
		return nil
	})
}

func exists(context *Context, key []byte) error {
	return context.WithinRead(func(txn *mdb.Txn) error {
		_, err := txn.Get(context.dbi, key)
		if err != nil {
			context.Output([]byte("false"))
		} else {
			context.Output([]byte("true"))
		}
		return nil
	})
}

func del(context *Context, key []byte) error {
	err := context.WithinWrite(func(txn *mdb.Txn) error {
		return txn.Del(context.dbi, key, nil)
	})
	if err != nil {
		return err
	}
	context.Output(OK)
	return nil
}

func put(context *Context, key, val []byte) error {
	err := context.WithinWrite(func(txn *mdb.Txn) error {
		return txn.Put(context.dbi, key, val, 0)
	})
	if err != nil {
		return err
	}
	context.Output(OK)
	return nil
}

func scan(context *Context, val []byte) error {
	if err := context.PrepareCursor(val); err != nil {
		return err
	}
	return iterate(context, true)
}

func iterate(context *Context, first bool) error {
	cursor := context.cursor
	if cursor == nil {
		return nil
	}
	for i := 0; i < 10; i++ {
		var err error
		var key, value []byte
		if first && cursor.prefix != nil {
			key, value, err = cursor.Get(cursor.prefix, nil, mdb.SET_RANGE)
			first = false
		} else {
			key, value, err = cursor.Get(nil, nil, mdb.NEXT)
		}

		if err == mdb.NotFound || (cursor.prefix != nil && !bytes.HasPrefix(key, cursor.prefix)) {
			context.CloseCursor()
			return nil
		}
		if err != nil {
			context.CloseCursor()
			return err
		}
		context.Output(key)
		context.Output(value)
	}
	context.Output(SCAN_MORE)
	return nil
}

// handle both space delimiters and arguments in quotations
// arguments are defined as contained by spaces ' arg ' or quotations '"arg"'
// forward slash escapes for nested quotations
func parseInput(in []byte) [][]byte {
	var results [][]byte
	var arg []byte
	state := STATE_NONE
	for _, b := range in {
		switch state {
		case STATE_NONE:
			if isQuote(b) {
				state = STATE_QUOTE
			} else if !isWhiteSpace(b) {
				arg = append(arg, b)
				state = STATE_WORD
			}
		case STATE_ESCAPED:
			arg = append(arg, b)
			state = STATE_QUOTE
		case STATE_WORD:
			if isWhiteSpace(b) {
				results = append(results, arg)
				arg = make([]byte, 0)
				state = STATE_NONE
			} else {
				arg = append(arg, b)
			}
		case STATE_QUOTE:
			if b == '\\' {
				state = STATE_ESCAPED
			} else if isQuote(b) {
				results = append(results, arg)
				arg = make([]byte, 0)
				state = STATE_NONE
			} else {
				arg = append(arg, b)
			}
		}
	}
	return results
}

func isWhiteSpace(b byte) bool {
	return unicode.IsSpace(rune(b))
}

func isQuote(b byte) bool {
	return b == '"' || b == '\'' || b == '`'
}

func getCommand(args [][]byte) (Command, error) {
	numArgs := 0
	var cmd Command
	if len(args) == 0 {
		return cmd, errors.New("empty command")
	}
	fn := string(args[0])
	min, exists := requiredArgs[fn]
	if !exists {
		return cmd, errors.New("invalid command")
	}
	var key, value []byte
	if len(args) >= 2 && len(args[1]) > 0 {
		key = args[1]
		numArgs++
	}
	if min > 1 && len(args) >= 3 && len(args[2]) > 0 {
		value = args[2]
		numArgs++
	}
	if numArgs < min {
		return cmd, errors.New("not enough arguments")
	}
	var extra [][]byte
	if len(args) > numArgs+1 {
		extra = args[numArgs+1:]
	}
	var jsonPrint bool
	for _, item := range extra {
		if string(item) == "json" {
			jsonPrint = true
		}
	}
	return Command{
		fn:        fn,
		key:       key,
		val:       value,
		jsonPrint: jsonPrint,
	}, nil
}
