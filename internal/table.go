package internal

import (
	"errors"
	"github.com/gdamore/tcell"
	"github.com/rivo/tview"
	"strings"
)

var	(
	errHeaderMismatched = errors.New("header and body mismatched")
)

type table interface {
	// Len returns the number of rows
	Len() int

	// ColumnNum returns the number of columns
	ColumnsNum() int

	// Header returns the header of the table
	Header() []string

	// Columns returns the content of a row
	Row(index int) []string

	// AddRow adds a row to table at index index, if index = -1,
	// appends the row to the end
	AddRow(index int, row []string) error

	// Remove removes row at index row
	Remove(row int)

	// SenContent updates the content in (row, column)
	SetContent(row, column int, content string)

	// Proportion returns the width proportion of the column
	Proportion(column int) int

	// Clear clears the body of the table
	Clear()
}

type SimpleTable struct {
	headers []string

	proportion []int

	rows [][]string
}

func (s *SimpleTable) Len() int {
	return len(s.rows)
}

func (s *SimpleTable) ColumnsNum() int {
	return len(s.headers)
}

func (s *SimpleTable) AddHeader(header string, proportion int)  {
	s.headers = append(s.headers, header)
	s.proportion = append(s.proportion, proportion)
}

func (s *SimpleTable) Header() []string  {
	headers := make([]string, len(s.headers))
	copy(headers, s.headers)
	return headers
}

func (s *SimpleTable) Row(index int) []string {
	if index >= len(s.rows) || index < 0 {
		return nil
	}
	return s.rows[index]
}

func (s *SimpleTable) AddRow(index int, row []string) error {
	if len(row) != len(s.headers) {
		return errHeaderMismatched
	}

	if index < 0 || index >= len(s.rows) {
		s.rows = append(s.rows, row)
		return nil
	}

	// make sure there are enough room to copy
	s.rows = append(s.rows, nil)
	copy(s.rows[index+1: ], s.rows[index:])
	s.rows[index] = row
	return nil
}

func (s *SimpleTable) Remove(row int)  {
	if row >= len(s.rows) || len(s.rows) == 0 {
		return
	}

	if row < 0 {
		row = len(s.rows) - 1
	}

	copy(s.rows[row:], s.rows[row+1:])
	s.rows = s.rows[:len(s.rows)-1]
}

func (s *SimpleTable) SetContent(row, col int, content string)  {
	if row >= len(s.rows) || col >= len(s.headers) {
		return
	}
	if row < 0 {
		row = len(s.rows) - 1
	}

	if col < 0 {
		col = len(s.headers) - 1
	}

	s.rows[row][col] = content
}

func (s *SimpleTable) Proportion(index int) int {
	if index >= len(s.headers) {
		return 0
	} else if index < 0 {
		index = len(s.headers) - 1
	}
	return s.proportion[index]
}

func (s *SimpleTable) Clear() {
	// s.headers = make([]string, 0)
	// s.proportion = make([]int, 0)
	s.rows = make([][]string, 0)
}



type TableView struct {
	*tview.Box

	tb table

	// top indicates the top row index
	top int

	// bottom indicates the bottom row index of the current page
	bottom int

	// onChanged will be called when fields in a row has changed
	onChanged func(tb *TableView, row int)

}


func (t *TableView) AddRows(rows ...[]string) error {
	for _, r := range rows {
		err := t.tb.AddRow(-1, r)
		if t.onChanged != nil {
			t.onChanged(t, -1)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *TableView) InsertRow(index int, row []string) error {
	return t.tb.AddRow(index, row)
}


func (t *TableView) Draw(screen tcell.Screen)  {
	t.Box.Draw(screen)

	x, y, width, height := t.GetInnerRect()
	bottomLimit := y + height

	// FIXME: whether this is a bug? if x=1 the first character would be cover or show on the right most?
	x += 2
	width -= 2

	cols := t.tb.ColumnsNum()
	if cols == 0 {
		return
	}

	// determine column width
	// proportion sum
	pptSum := 0
	for i := 0; i < cols; i++ {
		pptSum += t.tb.Proportion(i)
	}
	if width <= 1 {
		return
	}

	buf := &strings.Builder{}
	for idx, item := range t.tb.Header() {
		ppt := t.tb.Proportion(idx)
		colWidth := width * ppt / pptSum
		l := len(item)
		if l > colWidth {
			buf.WriteString(item[:colWidth-1])
		} else {
			buf.WriteString(item)
			for i := 0; i < (colWidth - l); i++ {
				buf.WriteString(" ")
			}
		}
	}

	tview.Print(screen, buf.String(), x, y, width, tview.AlignLeft, tcell.ColorDeepSkyBlue)
	y++

	overflowed := false
	for idx := 0; idx < t.tb.Len(); idx++ {
		if idx < t.top {
			continue
		}

		if y >= bottomLimit {
			overflowed = true
			break
		}

		cursor := x
		for i, v := range t.tb.Row(idx) {
			colWidth := width * t.tb.Proportion(i) / pptSum
			tview.Print(screen, v, cursor, y, colWidth, tview.AlignLeft, tcell.ColorDefault)
			cursor += colWidth
		}
		t.bottom = idx
		y++
	}
	if !overflowed {
		t.bottom = -1
	}
}

func (t *TableView) UpdateRow(row, col int, content string) {
	t.tb.SetContent(row, col, content)
	if t.onChanged != nil {
		t.onChanged(t, row)
	}
}

func (t *TableView) RemoveRow(row int) {
	t.tb.Remove(row)
	if t.onChanged != nil {
		t.onChanged(t, row)
	}
}

func (t *TableView) NextPage() {
	if t.bottom < 0 {
		return
	}
	t.top = t.bottom + 1
}

func (t *TableView) PrevPage() {
	_, _, _, height := t.GetInnerRect()
	height--
	newTop := t.top - height
	if newTop > 0 {
		t.top = newTop
	} else {
		t.top = 0
	}
}


func (t *TableView) SetOnChangedFunc(f func(*TableView, int)) {
	t.onChanged = f
}

func (t *TableView) Clear() {
	t.tb.Clear()
	t.top = 0
}


func SimpleTableView(headers []string, proportion []int) (*TableView, error) {
	if len(headers) != len(proportion) {
			return nil, errHeaderMismatched
		}
	tb := &SimpleTable{
		headers:    headers,
		proportion: proportion,
		rows:       make([][]string, 0),
	}
	view := NewTableView(tb)
	return view, nil
}

func NewTableView(tb table) *TableView {
	t := &TableView{
		Box:       tview.NewBox(),
		tb:        tb,
		bottom:    -1,
	}
	return t
}