// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell"
)

var (
	// ErrUserPressedEscape is returned from blocking UI methods that expect
	// input, but the user pressed escape.
	ErrUserPressedEscape = fmt.Errorf("Esc pressed")
)

// Status is an interface that allows you to update the status message when
// inside a UI.WithStatus callback
type Status interface {
	Update(msg string, args ...interface{})
}

// UI provides methods for an interactive user interface.
type UI interface {
	Enter(name string, work func() error) error
	ShowMenu(title string, options []string) (int, error)
	ShowForm(title string, options []TextField) error
	ShowMessage(title, msg string, args ...interface{})
	ShowConfirmation(title, msg, question string) (bool, error)
	WithStatus(msg string, work func(Status) error) error
	Terminate()
}

// New returns a new UI.
func New() UI {
	s, err := tcell.NewScreen()
	if err != nil || s == nil {
		return stdUI{}
	}
	s.Init()
	return &tcellUI{Screen: s}
}

// TextField holds fields of a UI text input field.
type TextField struct {
	// Name of the field presented to the user.
	Name string

	// Pointer to the value of the field, updated by the user.
	Value *string

	// Optional validation function for the field.
	Validate func(string) error
}

func (f TextField) text(highlighted bool) string {
	if highlighted {
		return *f.Value + "_"
	}
	return *f.Value
}

func (f TextField) color() tcell.Color {
	if f.validate() != nil {
		return tcell.ColorRed
	}
	return tcell.ColorDefault
}

func (f *TextField) input(k tcell.Key, r rune) {
	switch k {
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if r, n := utf8.DecodeLastRuneInString(*f.Value); r != utf8.RuneError {
			*f.Value = (*f.Value)[:len(*f.Value)-n]
		}
	case tcell.KeyEnter:
	default:
		if r != 0 && r != '\n' {
			*f.Value += string([]rune{r})
		}
	}
}

func (f TextField) validate() error {
	if f.Validate == nil {
		return nil
	}
	return f.Validate(*f.Value)
}

////////////////////////////////////////////////////////////////////////////////
// stdUI
////////////////////////////////////////////////////////////////////////////////
type stdUI struct{}

func (stdUI) Enter(name string, work func() error) error {
	return work()
}

func (stdUI) ShowMenu(title string, options []string) (int, error) {
	fmt.Printf("%v\n", title)
	for i, o := range options {
		fmt.Printf("  (%v): %v\n", i, o)
	}
	for true {
		fmt.Printf("\nEnter option [0-%d]: ", len(options)-1)
		i := -1
		_, err := fmt.Scan(&i)
		if err != nil {
			continue
		}
		if i < 0 || i >= len(options) {
			fmt.Printf("\n%d is not an option.\n", i)
			continue
		}
		return i, nil
	}
	panic("unreachable")
}

func (stdUI) ShowForm(title string, options []TextField) error {
	fmt.Printf("%v", title)
	for i, o := range options {
		for true {
			fmt.Printf("\n  %v: %v", o.Name, o.Value)

			in := ""
			fmt.Scan(&in)
			if o.Validate != nil {
				if err := o.Validate(in); err != nil {
					fmt.Printf("\n%v", err)
					continue
				}
			}
			*options[i].Value = in
			break
		}
	}
	return nil
}

func (stdUI) ShowMessage(title, msg string, args ...interface{}) {
	fmt.Printf("%s\n\n", title)
	fmt.Printf(msg, args...)
	fmt.Printf("\n\nPress enter to continue")
	in := ""
	fmt.Scan(&in)
}

func (stdUI) ShowConfirmation(title, msg, question string) (bool, error) {
	fmt.Printf("%s\n\n", title)
	fmt.Printf(msg)
	fmt.Println()
	for true {
		fmt.Printf("\n%v [y,n]:", question)
		in := ""
		fmt.Scan(&in)
		switch in {
		case "y", "Y", "yes", "Yes", "YES":
			return true, nil
		case "n", "N", "no", "No", "NO":
			return false, nil
		}
	}
	panic("unreachable")
}

type stdStatus struct{}

func (stdStatus) Update(msg string, args ...interface{}) {
	fmt.Printf(msg+"\n", args...)
}

func (stdUI) WithStatus(msg string, work func(Status) error) error {
	fmt.Println(msg)
	return work(stdStatus{})
}

func (stdUI) SetStatus(msg string, args ...interface{}) {
	fmt.Printf(msg+"\n", args...)
}

func (stdUI) Terminate() {}

type tcellUI struct {
	tcell.Screen
	status      string
	breadcrumbs []string
}

////////////////////////////////////////////////////////////////////////////////
// tcellUI
////////////////////////////////////////////////////////////////////////////////

func (u *tcellUI) Enter(name string, work func() error) error {
	u.breadcrumbs = append(u.breadcrumbs, name)
	err := work()
	u.breadcrumbs = u.breadcrumbs[:len(u.breadcrumbs)-1]
	return err
}

func (u *tcellUI) ShowMenu(title string, options []string) (int, error) {
	selected := -1
	err := u.drawPaged(title, len(options),
		func(l int, highlighted bool) (string, string, tcell.Color) {
			return options[l], "", tcell.ColorDefault
		},
		func(l int, k tcell.Key, r rune) (done bool) {
			if k == tcell.KeyEnter {
				selected = l
				return true
			}
			return false
		})
	return selected, err
}

func (u *tcellUI) ShowForm(title string, fields []TextField) error {
	columnWidth := 0
	for _, f := range fields {
		columnWidth = max(columnWidth, strlen(f.Name))
	}
	confirmIdx := len(fields)
	return u.drawPaged(title, len(fields)+1,
		func(i int, highlighted bool) (string, string, tcell.Color) {
			switch i {
			case confirmIdx:
				for _, f := range fields {
					if f.validate() != nil {
						return "[Confirm]", "Some fields contain invalud input", tcell.ColorDimGray
					}
				}
				return "[Confirm]", "", tcell.ColorDefault
			default:
				status := ""
				err := fields[i].validate()
				if err != nil {
					status = err.Error()
				}
				text := align(fields[i].Name, columnWidth) + ": " + fields[i].text(highlighted)
				return text, status, fields[i].color()
			}
		},
		func(i int, k tcell.Key, r rune) (done bool) {
			switch i {
			case confirmIdx:
				if k == tcell.KeyEnter || r == '\n' {
					for _, f := range fields {
						if f.validate() != nil {
							return false
						}
					}
					return true
				}
				return false
			default:
				fields[i].input(k, r)
				return false
			}
		})
}

func (u *tcellUI) ShowMessage(title, msg string, args ...interface{}) {
	lines := strings.Split(fmt.Sprintf(msg, args...), "\n")
	u.drawPaged(title, len(lines),
		func(idx int, highlighted bool) (string, string, tcell.Color) {
			return lines[idx], "", tcell.ColorDefault
		},
		func(line int, key tcell.Key, r rune) (done bool) {
			return key == tcell.KeyEnter || r == '\n'
		})
}

func (u *tcellUI) ShowConfirmation(title, msg, question string) (bool, error) {
	u.ShowMessage(title, msg)
	i, err := u.ShowMenu(question, []string{"no", "yes"})
	if err != nil {
		return false, err
	}
	return i == 1, nil
}

type tcellStatus struct{ u *tcellUI }

func (s tcellStatus) Update(msg string, args ...interface{}) {
	s.u.status = fmt.Sprintf(msg, args...)
	s.u.present()
}

func (u *tcellUI) WithStatus(msg string, work func(Status) error) error {
	oldStatus := u.status
	u.status = msg
	u.present()
	err := work(tcellStatus{u})
	u.status = oldStatus
	u.present()
	return err
}

func (u *tcellUI) Terminate() { u.Fini() }

func (u *tcellUI) drawPaged(title string, lines int,
	line func(idx int, highlighted bool) (text, status string, color tcell.Color),
	input func(line int, key tcell.Key, r rune) (done bool)) error {

	defer u.Clear()

	highlighted, scroll := 0, 0
	for true {
		u.Clear()

		_, h := u.Size()
		h -= 3 // app title, status, and off by one reported by Size()
		if h > 0 {
			u.SetContent(1, 2, ' ', []rune(title), tcell.StyleDefault)
			h-- // paged title

			if h > 0 {
				highlighted = clamp(highlighted, 0, lines-1)
				if highlighted < scroll {
					scroll = highlighted
				}
				if highlighted >= scroll+h {
					scroll = highlighted - h + 1
					if scroll < 0 {
						scroll = 0
					}
				}
				u.status = ""
				for i := 0; i < h && i+scroll < lines; i++ {
					idx := i + scroll
					l, s, col := line(idx, idx == highlighted)
					if idx == highlighted {
						l = "> " + l
						u.status = s
					} else {
						l = "  " + l
					}
					u.SetContent(2, i+3, ' ', []rune(l), tcell.StyleDefault.Bold(idx == highlighted).Foreground(col))
				}
			}
		}

		u.present()

		switch event := u.PollEvent().(type) {
		case *tcell.EventKey:
			switch event.Key() {
			case tcell.KeyEsc:
				return ErrUserPressedEscape
			case tcell.KeyUp:
				if highlighted > 0 {
					highlighted--
				}
			case tcell.KeyDown:
				if highlighted < lines-1 {
					highlighted++
				}
			default:
				if input(highlighted, event.Key(), event.Rune()) {
					return nil
				}
			}
		}
	}
	panic("unreachable")
}

func (u *tcellUI) present() {
	_, h := u.Size()
	breadcrumbs := strings.Join(u.breadcrumbs, " > ")
	if breadcrumbs != "" {
		breadcrumbs = fmt.Sprintf("[%v] ", breadcrumbs)
	}
	title := fmt.Sprintf("--- Release Me %v---", breadcrumbs)
	u.SetContent(1, 1, ' ', []rune(title), tcell.StyleDefault)
	u.SetContent(1, h-1, ' ', []rune(u.status), tcell.StyleDefault.Dim(true))
	u.Sync()
}

////////////////////////////////////////////////////////////////////////////////
// utils
////////////////////////////////////////////////////////////////////////////////
func max(x, y int) int {
	if x < y {
		return y
	}
	return x
}
func clamp(x, min, max int) int {
	if x < min {
		return min
	}
	if x > max {
		return max
	}
	return x
}
func align(s string, width int) string { return strings.Repeat(" ", max(width-strlen(s), 0)) + s }
func strlen(s string) int              { return utf8.RuneCountInString(s) }
