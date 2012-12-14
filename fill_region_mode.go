package main

import (
	"strconv"
	"bytes"
)

type fill_region_context struct {
	g *godit
	prefix []byte
	maxv int
}

// just a couple of prefixes for popular languages, sorted from long to short
var fill_region_prefixes = [][]byte{
	[]byte(";;;;"), // Lisp
	[]byte(";;;"), // Lisp
	[]byte("REM"), // cmd.exe, COMMAND.COM, Basic
	[]byte("//"), // C, C++, C#, D, Go, Java, JavaScript, Delphi, PHP, etc.
	[]byte(";;"), // Lisp
	[]byte("--"), // Haskell, Lua, Ada, SQL, etc.
	[]byte("::"), //md.exe, COMMAND.COM, Basic
	[]byte("#"), // Perl, Python, Ruby, Bash, PHP, etc.
	[]byte(";"), // Lisp
	[]byte(":"), // cmd.exe, COMMAND.COM, Basic
	// TODO: more?
}

func (f *fill_region_context) maxv_lemp() line_edit_mode_params {
	v := f.g.active.leaf
	return line_edit_mode_params{
		prompt: "Fill width:",
		initial_content: "80",
		on_apply: func(buf *buffer) {
			if i, err := strconv.Atoi(string(buf.contents())); err == nil {
				f.maxv = i
			}
			v.finalize_action_group()
			v.last_vcommand = vcommand_none
			v.fill_region(f.maxv, f.prefix)
			v.finalize_action_group()
		},
	}
}

func (f *fill_region_context) prefix_lemp() line_edit_mode_params {
	return line_edit_mode_params{
		prompt: "Prefix:",
		initial_content: string(f.prefix),
		on_apply: func(buf *buffer) {
			f.prefix = buf.contents()
			f.g.set_overlay_mode(init_line_edit_mode(f.g, f.maxv_lemp()))
		},
	}
}

func init_fill_region_mode(godit *godit) *line_edit_mode {
	v := godit.active.leaf
	f := fill_region_context{g: godit, maxv: 80}
	beg, _ := v.line_region()
	data := beg.line.data
	data = data[index_first_non_space(data):]
	for _, prefix := range fill_region_prefixes {
		if bytes.HasPrefix(data, prefix) {
			f.prefix = prefix
		}
	}
	return init_line_edit_mode(godit, f.prefix_lemp())
}
