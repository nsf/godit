package main

import (
	"bytes"
	"github.com/nsf/tulib"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
)

func readdir_stat(dir string, f *os.File) ([]os.FileInfo, error) {
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}

	fis := make([]os.FileInfo, len(names))
	for i, name := range names {
		fis[i], err = os.Stat(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
	}
	return fis, nil
}

func index_first_non_space(s []byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] != '\t' && s[i] != ' ' {
			return i
		}
	}
	return -1
}

func index_last_non_space(s []byte) int {
	for i := len(s)-1; i >= 0; i-- {
		if s[i] != '\t' && s[i] != ' ' {
			return i
		}
	}
	return -1
}

func abs_path(filename string) string {
	path, err := filepath.Abs(filename)
	if err != nil {
		panic(err)
	}
	return path
}

func grow_byte_slice(s []byte, desired_cap int) []byte {
	if cap(s) < desired_cap {
		ns := make([]byte, len(s), desired_cap)
		copy(ns, s)
		return ns
	}
	return s
}

func insert_bytes(s []byte, offset int, data []byte) []byte {
	n := len(s) + len(data)
	s = grow_byte_slice(s, n)
	s = s[:n]
	copy(s[offset+len(data):], s[offset:])
	copy(s[offset:], data)
	return s
}

func copy_byte_slice(s []byte, b, e int) []byte {
	c := make([]byte, e-b)
	copy(c, s[b:e])
	return c
}

func clone_byte_slice(s []byte) []byte {
	c := make([]byte, len(s))
	copy(c, s)
	return c
}

// assumes the same line and a.boffset < b.offset order
func bytes_between(a, b cursor_location) []byte {
	return a.line.data[a.boffset:b.boffset]
}

func is_word(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

func find_place_for_rect(win, pref tulib.Rect) tulib.Rect {
	var vars [4]tulib.Rect

	vars[0] = pref.Intersection(win)
	if vars[0] == pref {
		// this is just a common path, everything fits in
		return pref
	}

	// If a rect doesn't fit in the window, try to select the most
	// optimal position amongst mirrored variants.

	// invert X
	vars[1] = pref
	vars[1].X = win.Width - pref.Width
	vars[1] = vars[1].Intersection(win)

	// invert Y
	vars[2] = pref
	vars[2].Y -= pref.Height + 1
	vars[2] = vars[2].Intersection(win)

	// invert X and Y
	vars[3] = pref
	vars[3].X = win.Width - pref.Width
	vars[3].Y -= pref.Height + 1
	vars[3] = vars[3].Intersection(win)

	optimal_i, optimal_w, optimal_h := 0, 0, 0
	// find optimal width
	for i := 0; i < 4; i++ {
		if vars[i].Width > optimal_w {
			optimal_w = vars[i].Width
		}
	}

	// find optimal height (amongst optimal widths) and its index
	for i := 0; i < 4; i++ {
		if vars[i].Width != optimal_w {
			continue
		}
		if vars[i].Height > optimal_h {
			optimal_h = vars[i].Height
			optimal_i = i
		}
	}
	return vars[optimal_i]
}

// Function will iterate 'data' contents, calling 'cb' on some data or on '\n',
// but never both. For example, given this data: "\n123\n123\n\n", it will call
// 'cb' 6 times: ['\n', '123', '\n', '123', '\n', '\n']
func iter_lines(data []byte, cb func([]byte)) {
	offset := 0
	for {
		if offset == len(data) {
			return
		}

		i := bytes.IndexByte(data[offset:], '\n')
		switch i {
		case -1:
			cb(data[offset:])
			return
		case 0:
			cb(data[offset : offset+1])
			offset++
			continue
		}

		cb(data[offset : offset+i])
		cb(data[offset+i : offset+i+1])
		offset += i + 1
	}
}

var double_comma = []byte(",,")

func split_double_csv(data []byte) (a, b []byte) {
	i := bytes.Index(data, double_comma)
	if i == -1 {
		return data, nil
	}

	return data[:i], data[i+2:]
}

type line_reader struct {
	data   []byte
	offset int
}

func new_line_reader(data []byte) line_reader {
	return line_reader{data, 0}
}

func (l *line_reader) read_line() []byte {
	data := l.data[l.offset:]
	i := bytes.Index(data, []byte{'\n'})
	if i == -1 {
		l.offset = len(l.data)
		return data
	}

	l.offset += i + 1
	return data[:i]
}

func atoi(data []byte) (int, error) {
	return strconv.Atoi(string(data))
}

func substitute_home(path string) string {
	if !strings.HasPrefix(path, "~") {
		return path
	}
	home := os.Getenv("HOME")
	if home == "" {
		panic("HOME is not set")
	}
	return filepath.Join(home, path[1:])
}

func substitute_symlinks(path string) string {
	if path == "" {
		return ""
	}
	after, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}

	if strings.HasSuffix(path, string(filepath.Separator)) {
		return after + string(filepath.Separator)
	}
	return after
}

func is_file_hidden(path string) bool {
	if path == "." || path == ".." {
		return true
	}

	if len(path) > 1 {
		if strings.HasPrefix(path, "./") {
			return false
		}
		if strings.HasPrefix(path, "..") {
			return false
		}
		if strings.HasPrefix(path, ".") {
			return true
		}
	}
	return false
}