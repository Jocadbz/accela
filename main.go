package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/gdamore/tcell/v2"
	"golang.design/x/clipboard"
)

type Selection struct {
	StartLine, StartCol int
	EndLine, EndCol     int
	Active              bool
}

type TokenInfo struct {
	Col   int
	Len   int
	Style tcell.Style
}

type Buffer struct {
	Lines      []string
	Filename   string
	CursorX    int
	CursorY    int
	OffsetX    int
	OffsetY    int
	Selection  Selection
	Lexer      chroma.Lexer
	Style      *chroma.Style
	TokenCache [][]TokenInfo
	Dirty      bool
}

type SplitType int

const (
	SplitNone SplitType = iota
	SplitHorizontal
	SplitVertical
)

type Pane struct {
	Buffer *Buffer
	X, Y   int
	Width  int
	Height int
}

type SearchMatch struct {
	Line int
	Col  int
	Len  int
}

type Editor struct {
	Screen        tcell.Screen
	Panes         []*Pane
	ActivePane    int
	SplitType     SplitType
	CommandMode   bool
	Command       string
	StatusMsg     string
	SearchMode    bool
	SearchQuery   string
	SearchMatches []SearchMatch
	SearchIndex   int
}

func NewBuffer() *Buffer {
	return &Buffer{
		Lines: []string{""},
		Style: styles.Get("monokai"),
	}
}

func (b *Buffer) SetupHighlighting() {
	if b.Filename == "" {
		b.Lexer = lexers.Fallback
		return
	}
	b.Lexer = lexers.Match(b.Filename)
	if b.Lexer == nil {
		b.Lexer = lexers.Fallback
	}
	b.Lexer = chroma.Coalesce(b.Lexer)
	b.UpdateTokenCache()
}

func chromaToTcell(c chroma.Colour) tcell.Color {
	if !c.IsSet() {
		return tcell.ColorDefault
	}
	return tcell.NewRGBColor(int32(c.Red()), int32(c.Green()), int32(c.Blue()))
}

func (b *Buffer) UpdateTokenCache() {
	if b.Lexer == nil || b.Style == nil {
		return
	}
	
	content := strings.Join(b.Lines, "\n")
	iterator, err := b.Lexer.Tokenise(nil, content)
	if err != nil {
		return
	}
	
	b.TokenCache = make([][]TokenInfo, len(b.Lines))
	for i := range b.TokenCache {
		b.TokenCache[i] = []TokenInfo{}
	}
	
	lineNum := 0
	col := 0
	
	for _, token := range iterator.Tokens() {
		entry := b.Style.Get(token.Type)
		fg := chromaToTcell(entry.Colour)
		style := tcell.StyleDefault.Foreground(fg)
		if entry.Bold == chroma.Yes {
			style = style.Bold(true)
		}
		if entry.Italic == chroma.Yes {
			style = style.Italic(true)
		}
		
		tokenLines := strings.Split(token.Value, "\n")
		for i, part := range tokenLines {
			if i > 0 {
				lineNum++
				col = 0
			}
			if lineNum < len(b.TokenCache) && len(part) > 0 {
				b.TokenCache[lineNum] = append(b.TokenCache[lineNum], TokenInfo{
					Col:   col,
					Len:   len(part),
					Style: style,
				})
			}
			col += len(part)
		}
	}
}

func (b *Buffer) GetStyleAt(line, col int) tcell.Style {
	if line >= len(b.TokenCache) {
		return tcell.StyleDefault
	}
	for _, token := range b.TokenCache[line] {
		if col >= token.Col && col < token.Col+token.Len {
			return token.Style
		}
	}
	return tcell.StyleDefault
}

func (b *Buffer) LoadFile(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			b.Filename = filename
			b.Lines = []string{""}
			b.SetupHighlighting()
			return nil
		}
		return err
	}
	b.Filename = filename
	content := string(data)
	content = strings.ReplaceAll(content, "\r\n", "\n")
	b.Lines = strings.Split(content, "\n")
	if len(b.Lines) == 0 {
		b.Lines = []string{""}
	}
	b.SetupHighlighting()
	return nil
}

func (b *Buffer) SetFilename(filename string) {
	b.Filename = filename
	b.SetupHighlighting()
}

func (b *Buffer) MarkDirty() {
	b.Dirty = true
}

func (b *Buffer) RefreshIfDirty() {
	if b.Dirty {
		b.UpdateTokenCache()
		b.Dirty = false
	}
}

func (b *Buffer) SaveFile() error {
	if b.Filename == "" {
		return fmt.Errorf("no filename")
	}
	content := strings.Join(b.Lines, "\n")
	return os.WriteFile(b.Filename, []byte(content), 0644)
}

func (b *Buffer) GetSelectedText() string {
	if !b.Selection.Active {
		return ""
	}
	startLine, startCol := b.Selection.StartLine, b.Selection.StartCol
	endLine, endCol := b.Selection.EndLine, b.Selection.EndCol
	
	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	
	if startLine == endLine {
		line := b.Lines[startLine]
		if startCol > len(line) {
			startCol = len(line)
		}
		if endCol > len(line) {
			endCol = len(line)
		}
		return line[startCol:endCol]
	}
	
	var result strings.Builder
	for i := startLine; i <= endLine; i++ {
		line := b.Lines[i]
		if i == startLine {
			if startCol < len(line) {
				result.WriteString(line[startCol:])
			}
			result.WriteString("\n")
		} else if i == endLine {
			if endCol > len(line) {
				endCol = len(line)
			}
			result.WriteString(line[:endCol])
		} else {
			result.WriteString(line)
			result.WriteString("\n")
		}
	}
	return result.String()
}

func (b *Buffer) DeleteSelection() {
	if !b.Selection.Active {
		return
	}
	startLine, startCol := b.Selection.StartLine, b.Selection.StartCol
	endLine, endCol := b.Selection.EndLine, b.Selection.EndCol
	
	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	
	if startCol > len(b.Lines[startLine]) {
		startCol = len(b.Lines[startLine])
	}
	if endCol > len(b.Lines[endLine]) {
		endCol = len(b.Lines[endLine])
	}
	
	beforeStart := b.Lines[startLine][:startCol]
	afterEnd := b.Lines[endLine][endCol:]
	
	newLines := make([]string, 0)
	newLines = append(newLines, b.Lines[:startLine]...)
	newLines = append(newLines, beforeStart+afterEnd)
	newLines = append(newLines, b.Lines[endLine+1:]...)
	
	b.Lines = newLines
	b.CursorX = startCol
	b.CursorY = startLine
	b.Selection.Active = false
	b.MarkDirty()
}

func isWordChar(ch rune) bool {
	return unicode.IsLetter(ch) || unicode.IsDigit(ch) || ch == '_'
}

func (b *Buffer) MoveWordLeft() {
	runes := []rune(b.Lines[b.CursorY])
	if b.CursorX == 0 {
		if b.CursorY > 0 {
			b.CursorY--
			b.CursorX = len([]rune(b.Lines[b.CursorY]))
		}
		return
	}
	if b.CursorX > len(runes) {
		b.CursorX = len(runes)
	}
	// Skip whitespace/non-word chars going left
	for b.CursorX > 0 && !isWordChar(runes[b.CursorX-1]) {
		b.CursorX--
	}
	// Skip word chars going left
	for b.CursorX > 0 && isWordChar(runes[b.CursorX-1]) {
		b.CursorX--
	}
}

func (b *Buffer) MoveWordRight() {
	runes := []rune(b.Lines[b.CursorY])
	if b.CursorX >= len(runes) {
		if b.CursorY < len(b.Lines)-1 {
			b.CursorY++
			b.CursorX = 0
		}
		return
	}
	// Skip word chars going right
	for b.CursorX < len(runes) && isWordChar(runes[b.CursorX]) {
		b.CursorX++
	}
	// Skip whitespace/non-word chars going right
	for b.CursorX < len(runes) && !isWordChar(runes[b.CursorX]) {
		b.CursorX++
	}
}

func NewEditor() (*Editor, error) {
	screen, err := tcell.NewScreen()
	if err != nil {
		return nil, err
	}
	if err := screen.Init(); err != nil {
		return nil, err
	}
	
	err = clipboard.Init()
	if err != nil {
		screen.Fini()
		return nil, fmt.Errorf("clipboard init failed: %v", err)
	}
	
	w, h := screen.Size()
	buf := NewBuffer()
	pane := &Pane{
		Buffer: buf,
		X:      0,
		Y:      0,
		Width:  w,
		Height: h - 2,
	}
	
	return &Editor{
		Screen:     screen,
		Panes:      []*Pane{pane},
		ActivePane: 0,
		SplitType:  SplitNone,
	}, nil
}

func (e *Editor) CurrentPane() *Pane {
	return e.Panes[e.ActivePane]
}

func (e *Editor) CurrentBuffer() *Buffer {
	return e.CurrentPane().Buffer
}

func (e *Editor) UpdatePaneSizes() {
	w, h := e.Screen.Size()
	editHeight := h - 2
	
	if len(e.Panes) == 1 {
		e.Panes[0].X = 0
		e.Panes[0].Y = 0
		e.Panes[0].Width = w
		e.Panes[0].Height = editHeight
	} else if len(e.Panes) == 2 {
		if e.SplitType == SplitHorizontal {
			halfH := editHeight / 2
			e.Panes[0].X = 0
			e.Panes[0].Y = 0
			e.Panes[0].Width = w
			e.Panes[0].Height = halfH
			e.Panes[1].X = 0
			e.Panes[1].Y = halfH
			e.Panes[1].Width = w
			e.Panes[1].Height = editHeight - halfH
		} else {
			halfW := w / 2
			e.Panes[0].X = 0
			e.Panes[0].Y = 0
			e.Panes[0].Width = halfW
			e.Panes[0].Height = editHeight
			e.Panes[1].X = halfW
			e.Panes[1].Y = 0
			e.Panes[1].Width = w - halfW
			e.Panes[1].Height = editHeight
		}
	}
}

func (e *Editor) Draw() {
	e.Screen.Clear()
	e.UpdatePaneSizes()
	
	for i, pane := range e.Panes {
		e.DrawPane(pane, i == e.ActivePane)
	}
	
	e.DrawStatusBar()
	e.DrawCommandBar()
	e.Screen.Show()
}

func (e *Editor) DrawPane(pane *Pane, active bool) {
	buf := pane.Buffer
	buf.RefreshIfDirty()
	selStyle := tcell.StyleDefault.Background(tcell.ColorBlue).Foreground(tcell.ColorWhite)
	searchStyle := tcell.StyleDefault.Background(tcell.ColorYellow).Foreground(tcell.ColorBlack)
	
	for row := 0; row < pane.Height; row++ {
		lineIdx := buf.OffsetY + row
		if lineIdx >= len(buf.Lines) {
			for col := 0; col < pane.Width; col++ {
				e.Screen.SetContent(pane.X+col, pane.Y+row, ' ', nil, tcell.StyleDefault)
			}
			continue
		}
		
		runes := []rune(buf.Lines[lineIdx])
		for col := 0; col < pane.Width; col++ {
			charIdx := buf.OffsetX + col
			ch := ' '
			if charIdx < len(runes) {
				ch = runes[charIdx]
			}
			
			cellStyle := buf.GetStyleAt(lineIdx, charIdx)
			if buf.Selection.Active && e.isSelected(buf, lineIdx, charIdx) {
				cellStyle = selStyle
			} else if e.isSearchMatch(lineIdx, charIdx) {
				cellStyle = searchStyle
			}
			e.Screen.SetContent(pane.X+col, pane.Y+row, ch, nil, cellStyle)
		}
	}
	
	if active {
		cursorScreenX := pane.X + buf.CursorX - buf.OffsetX
		cursorScreenY := pane.Y + buf.CursorY - buf.OffsetY
		if cursorScreenX >= pane.X && cursorScreenX < pane.X+pane.Width &&
			cursorScreenY >= pane.Y && cursorScreenY < pane.Y+pane.Height {
			e.Screen.ShowCursor(cursorScreenX, cursorScreenY)
		}
	}
}

func (e *Editor) isSelected(buf *Buffer, line, col int) bool {
	if !buf.Selection.Active {
		return false
	}
	startLine, startCol := buf.Selection.StartLine, buf.Selection.StartCol
	endLine, endCol := buf.Selection.EndLine, buf.Selection.EndCol
	
	if startLine > endLine || (startLine == endLine && startCol > endCol) {
		startLine, endLine = endLine, startLine
		startCol, endCol = endCol, startCol
	}
	
	if line < startLine || line > endLine {
		return false
	}
	if line == startLine && line == endLine {
		return col >= startCol && col < endCol
	}
	if line == startLine {
		return col >= startCol
	}
	if line == endLine {
		return col < endCol
	}
	return true
}

func (e *Editor) isSearchMatch(line, col int) bool {
	for _, match := range e.SearchMatches {
		if match.Line == line && col >= match.Col && col < match.Col+match.Len {
			return true
		}
	}
	return false
}

func (e *Editor) DrawStatusBar() {
	w, h := e.Screen.Size()
	style := tcell.StyleDefault.Background(tcell.ColorGray).Foreground(tcell.ColorBlack)
	
	buf := e.CurrentBuffer()
	filename := buf.Filename
	if filename == "" {
		filename = "[No Name]"
	}
	status := fmt.Sprintf(" %s | Line %d/%d, Col %d ", filename, buf.CursorY+1, len(buf.Lines), buf.CursorX+1)
	
	for i := 0; i < w; i++ {
		ch := ' '
		if i < len(status) {
			ch = rune(status[i])
		}
		e.Screen.SetContent(i, h-2, ch, nil, style)
	}
}

func (e *Editor) DrawCommandBar() {
	w, h := e.Screen.Size()
	style := tcell.StyleDefault
	
	var text string
	if e.SearchMode {
		text = "/" + e.SearchQuery
	} else if e.CommandMode {
		text = "> " + e.Command
	} else if e.StatusMsg != "" {
		text = e.StatusMsg
	}
	
	for i := 0; i < w; i++ {
		ch := ' '
		if i < len(text) {
			ch = rune(text[i])
		}
		e.Screen.SetContent(i, h-1, ch, nil, style)
	}
	
	if e.CommandMode {
		e.Screen.ShowCursor(len(text), h-1)
	} else if e.SearchMode {
		e.Screen.ShowCursor(len(text), h-1)
	}
}

func (e *Editor) HandleEvent(ev tcell.Event) bool {
	switch ev := ev.(type) {
	case *tcell.EventResize:
		e.Screen.Sync()
		return true
	case *tcell.EventKey:
		return e.HandleKey(ev)
	}
	return true
}

func (e *Editor) HandleKey(ev *tcell.EventKey) bool {
	if e.CommandMode {
		return e.HandleCommandKey(ev)
	}
	if e.SearchMode {
		return e.HandleSearchKey(ev)
	}
	
	buf := e.CurrentBuffer()
	pane := e.CurrentPane()
	
	switch ev.Key() {
	case tcell.KeyEscape:
		buf.Selection.Active = false
		
	case tcell.KeyCtrlW:
		if len(e.Panes) > 1 {
			e.ActivePane = (e.ActivePane + 1) % len(e.Panes)
		}
		
	case tcell.KeyCtrlC:
		if buf.Selection.Active {
			text := buf.GetSelectedText()
			clipboard.Write(clipboard.FmtText, []byte(text))
			e.StatusMsg = "Copied to clipboard"
		}
		
	case tcell.KeyCtrlV:
		text := string(clipboard.Read(clipboard.FmtText))
		if text != "" {
			if buf.Selection.Active {
				buf.DeleteSelection()
			}
			e.InsertText(text)
			e.StatusMsg = "Pasted from clipboard"
		}
		
	case tcell.KeyCtrlX:
		if buf.Selection.Active {
			text := buf.GetSelectedText()
			clipboard.Write(clipboard.FmtText, []byte(text))
			buf.DeleteSelection()
			e.StatusMsg = "Cut to clipboard"
		}
		
	case tcell.KeyCtrlS:
		if err := buf.SaveFile(); err != nil {
			e.StatusMsg = fmt.Sprintf("Error: %v", err)
		} else {
			e.StatusMsg = "Saved"
		}
		
	case tcell.KeyCtrlE:
		e.CommandMode = true
		e.Command = ""
		
	case tcell.KeyCtrlF:
		e.SearchMode = true
		e.SearchQuery = ""
		e.SearchMatches = nil
		e.SearchIndex = 0
		
	case tcell.KeyUp:
		selecting := ev.Modifiers()&tcell.ModCtrl != 0
		if selecting && !buf.Selection.Active {
			buf.Selection.Active = true
			buf.Selection.StartLine = buf.CursorY
			buf.Selection.StartCol = buf.CursorX
		}
		if buf.CursorY > 0 {
			buf.CursorY--
			lineLen := len([]rune(buf.Lines[buf.CursorY]))
			if buf.CursorX > lineLen {
				buf.CursorX = lineLen
			}
		}
		if selecting {
			buf.Selection.EndLine = buf.CursorY
			buf.Selection.EndCol = buf.CursorX
		} else {
			buf.Selection.Active = false
		}
		e.ScrollToCursor(pane)
		
	case tcell.KeyDown:
		selecting := ev.Modifiers()&tcell.ModCtrl != 0
		if selecting && !buf.Selection.Active {
			buf.Selection.Active = true
			buf.Selection.StartLine = buf.CursorY
			buf.Selection.StartCol = buf.CursorX
		}
		if buf.CursorY < len(buf.Lines)-1 {
			buf.CursorY++
			lineLen := len([]rune(buf.Lines[buf.CursorY]))
			if buf.CursorX > lineLen {
				buf.CursorX = lineLen
			}
		}
		if selecting {
			buf.Selection.EndLine = buf.CursorY
			buf.Selection.EndCol = buf.CursorX
		} else {
			buf.Selection.Active = false
		}
		e.ScrollToCursor(pane)
		
	case tcell.KeyLeft:
		selecting := ev.Modifiers()&tcell.ModCtrl != 0
		wordJump := ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) == (tcell.ModCtrl|tcell.ModAlt)
		if (selecting || wordJump) && !buf.Selection.Active {
			buf.Selection.Active = true
			buf.Selection.StartLine = buf.CursorY
			buf.Selection.StartCol = buf.CursorX
		}
		if wordJump {
			buf.MoveWordLeft()
		} else if buf.CursorX > 0 {
			buf.CursorX--
		} else if buf.CursorY > 0 {
			buf.CursorY--
			buf.CursorX = len([]rune(buf.Lines[buf.CursorY]))
		}
		if selecting || wordJump {
			buf.Selection.EndLine = buf.CursorY
			buf.Selection.EndCol = buf.CursorX
		} else {
			buf.Selection.Active = false
		}
		e.ScrollToCursor(pane)
		
	case tcell.KeyRight:
		selecting := ev.Modifiers()&tcell.ModCtrl != 0
		wordJump := ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) == (tcell.ModCtrl|tcell.ModAlt)
		if (selecting || wordJump) && !buf.Selection.Active {
			buf.Selection.Active = true
			buf.Selection.StartLine = buf.CursorY
			buf.Selection.StartCol = buf.CursorX
		}
		lineLen := len([]rune(buf.Lines[buf.CursorY]))
		if wordJump {
			buf.MoveWordRight()
		} else if buf.CursorX < lineLen {
			buf.CursorX++
		} else if buf.CursorY < len(buf.Lines)-1 {
			buf.CursorY++
			buf.CursorX = 0
		}
		if selecting || wordJump {
			buf.Selection.EndLine = buf.CursorY
			buf.Selection.EndCol = buf.CursorX
		} else {
			buf.Selection.Active = false
		}
		e.ScrollToCursor(pane)
		
	case tcell.KeyEnter:
		if buf.Selection.Active {
			buf.DeleteSelection()
		}
		runes := []rune(buf.Lines[buf.CursorY])
		if buf.CursorX > len(runes) {
			buf.CursorX = len(runes)
		}
		buf.Lines[buf.CursorY] = string(runes[:buf.CursorX])
		newLine := string(runes[buf.CursorX:])
		buf.Lines = append(buf.Lines[:buf.CursorY+1], append([]string{newLine}, buf.Lines[buf.CursorY+1:]...)...)
		buf.CursorY++
		buf.CursorX = 0
		buf.MarkDirty()
		e.ScrollToCursor(pane)
		
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if buf.Selection.Active {
			buf.DeleteSelection()
		} else if buf.CursorX > 0 {
			runes := []rune(buf.Lines[buf.CursorY])
			if buf.CursorX > len(runes) {
				buf.CursorX = len(runes)
			}
			if buf.CursorX > 0 {
				buf.Lines[buf.CursorY] = string(runes[:buf.CursorX-1]) + string(runes[buf.CursorX:])
				buf.CursorX--
			}
		} else if buf.CursorY > 0 {
			prevRunes := []rune(buf.Lines[buf.CursorY-1])
			buf.CursorX = len(prevRunes)
			buf.Lines[buf.CursorY-1] = buf.Lines[buf.CursorY-1] + buf.Lines[buf.CursorY]
			buf.Lines = append(buf.Lines[:buf.CursorY], buf.Lines[buf.CursorY+1:]...)
			buf.CursorY--
		}
		buf.MarkDirty()
		e.ScrollToCursor(pane)
		
	case tcell.KeyDelete:
		if buf.Selection.Active {
			buf.DeleteSelection()
		} else {
			runes := []rune(buf.Lines[buf.CursorY])
			if buf.CursorX < len(runes) {
				buf.Lines[buf.CursorY] = string(runes[:buf.CursorX]) + string(runes[buf.CursorX+1:])
			} else if buf.CursorY < len(buf.Lines)-1 {
				buf.Lines[buf.CursorY] = buf.Lines[buf.CursorY] + buf.Lines[buf.CursorY+1]
				buf.Lines = append(buf.Lines[:buf.CursorY+1], buf.Lines[buf.CursorY+2:]...)
			}
		}
		buf.MarkDirty()
		
	case tcell.KeyTab:
		if buf.Selection.Active {
			buf.DeleteSelection()
		}
		e.InsertText("    ")
		
	case tcell.KeyRune:
		if ev.Rune() == 'n' && len(e.SearchMatches) > 0 {
			e.SearchIndex = (e.SearchIndex + 1) % len(e.SearchMatches)
			e.JumpToSearchMatch()
			return true
		}
		if ev.Rune() == 'N' && len(e.SearchMatches) > 0 {
			e.SearchIndex = (e.SearchIndex - 1 + len(e.SearchMatches)) % len(e.SearchMatches)
			e.JumpToSearchMatch()
			return true
		}
		if buf.Selection.Active {
			buf.DeleteSelection()
		}
		runes := []rune(buf.Lines[buf.CursorY])
		if buf.CursorX > len(runes) {
			buf.CursorX = len(runes)
		}
		buf.Lines[buf.CursorY] = string(runes[:buf.CursorX]) + string(ev.Rune()) + string(runes[buf.CursorX:])
		buf.CursorX++
		buf.MarkDirty()
		e.ScrollToCursor(pane)
	}
	
	return true
}

func (e *Editor) HandleCommandKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		e.CommandMode = false
		e.Command = ""
		
	case tcell.KeyEnter:
		e.ExecuteCommand()
		e.CommandMode = false
		e.Command = ""
		
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(e.Command) > 0 {
			e.Command = e.Command[:len(e.Command)-1]
		} else {
			e.CommandMode = false
		}
		
	case tcell.KeyTab:
		e.TabCompleteCommand()
		
	case tcell.KeyRune:
		e.Command += string(ev.Rune())
	}
	return true
}

func (e *Editor) TabCompleteCommand() {
	parts := strings.Fields(e.Command)
	if len(parts) == 0 {
		return
	}
	
	// Complete command name
	if len(parts) == 1 && !strings.HasSuffix(e.Command, " ") {
		commands := []string{"quit", "write", "wq", "edit", "hsplit", "vsplit", "close"}
		var matches []string
		for _, cmd := range commands {
			if strings.HasPrefix(cmd, parts[0]) {
				matches = append(matches, cmd)
			}
		}
		if len(matches) == 1 {
			e.Command = matches[0]
		}
		return
	}
	
	// Complete filename for e, hsplit, vsplit
	cmd := parts[0]
	if cmd != "e" && cmd != "edit" && cmd != "hsplit" && cmd != "vsplit" {
		return
	}
	
	var prefix string
	if len(parts) > 1 {
		prefix = parts[len(parts)-1]
	} else {
		prefix = ""
	}
	
	dir := filepath.Dir(prefix)
	if dir == "" || dir == "." {
		dir = "."
	}
	base := filepath.Base(prefix)
	if prefix == "" || strings.HasSuffix(prefix, "/") {
		if prefix != "" {
			dir = prefix
		}
		base = ""
	}
	
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	
	var matches []string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, base) {
			fullPath := filepath.Join(dir, name)
			if dir == "." {
				fullPath = name
			}
			if entry.IsDir() {
				fullPath += "/"
			}
			matches = append(matches, fullPath)
		}
	}
	
	if len(matches) == 1 {
		if len(parts) > 1 {
			e.Command = cmd + " " + matches[0]
		} else {
			e.Command = cmd + " " + matches[0]
		}
	}
}

func (e *Editor) HandleSearchKey(ev *tcell.EventKey) bool {
	switch ev.Key() {
	case tcell.KeyEscape:
		e.SearchMode = false
		e.SearchQuery = ""
		e.SearchMatches = nil
		
	case tcell.KeyEnter:
		e.ExecuteSearch()
		e.SearchMode = false
		
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if len(e.SearchQuery) > 0 {
			e.SearchQuery = e.SearchQuery[:len(e.SearchQuery)-1]
		} else {
			e.SearchMode = false
		}
		
	case tcell.KeyRune:
		e.SearchQuery += string(ev.Rune())
	}
	return true
}

func (e *Editor) ExecuteSearch() {
	if e.SearchQuery == "" {
		return
	}
	
	buf := e.CurrentBuffer()
	e.SearchMatches = nil
	
	for lineIdx, line := range buf.Lines {
		start := 0
		for {
			idx := strings.Index(line[start:], e.SearchQuery)
			if idx == -1 {
				break
			}
			e.SearchMatches = append(e.SearchMatches, SearchMatch{
				Line: lineIdx,
				Col:  start + idx,
				Len:  len(e.SearchQuery),
			})
			start += idx + 1
		}
	}
	
	if len(e.SearchMatches) > 0 {
		e.SearchIndex = 0
		e.JumpToSearchMatch()
		e.StatusMsg = fmt.Sprintf("Found %d matches", len(e.SearchMatches))
	} else {
		e.StatusMsg = "No matches found"
	}
}

func (e *Editor) JumpToSearchMatch() {
	if len(e.SearchMatches) == 0 {
		return
	}
	
	match := e.SearchMatches[e.SearchIndex]
	buf := e.CurrentBuffer()
	pane := e.CurrentPane()
	
	buf.CursorY = match.Line
	buf.CursorX = match.Col
	e.ScrollToCursor(pane)
	e.StatusMsg = fmt.Sprintf("Match %d/%d", e.SearchIndex+1, len(e.SearchMatches))
}

func (e *Editor) ExecuteCommand() {
	parts := strings.Fields(e.Command)
	if len(parts) == 0 {
		return
	}
	
	cmd := parts[0]
	args := parts[1:]
	
	switch cmd {
	case "q", "quit":
		e.Screen.Fini()
		os.Exit(0)
		
	case "w", "write":
		buf := e.CurrentBuffer()
		if len(args) > 0 {
			buf.Filename = args[0]
		}
		if err := buf.SaveFile(); err != nil {
			e.StatusMsg = fmt.Sprintf("Error: %v", err)
		} else {
			e.StatusMsg = fmt.Sprintf("Written: %s", buf.Filename)
		}
		
	case "wq":
		buf := e.CurrentBuffer()
		if err := buf.SaveFile(); err != nil {
			e.StatusMsg = fmt.Sprintf("Error: %v", err)
		} else {
			e.Screen.Fini()
			os.Exit(0)
		}
		
	case "e", "edit":
		if len(args) < 1 {
			e.StatusMsg = "Usage: :e <filename>"
			return
		}
		buf := e.CurrentBuffer()
		if err := buf.LoadFile(args[0]); err != nil {
			e.StatusMsg = fmt.Sprintf("Error: %v", err)
		} else {
			buf.CursorX = 0
			buf.CursorY = 0
			buf.OffsetX = 0
			buf.OffsetY = 0
			e.StatusMsg = fmt.Sprintf("Loaded: %s", args[0])
		}
		
	case "hsplit":
		if len(e.Panes) < 2 {
			newBuf := NewBuffer()
			if e.CurrentBuffer().Filename != "" {
				newBuf.LoadFile(e.CurrentBuffer().Filename)
			}
			newPane := &Pane{Buffer: newBuf}
			e.Panes = append(e.Panes, newPane)
			e.SplitType = SplitHorizontal
			e.StatusMsg = "Horizontal split"
		}
		
	case "vsplit":
		if len(e.Panes) < 2 {
			newBuf := NewBuffer()
			if e.CurrentBuffer().Filename != "" {
				newBuf.LoadFile(e.CurrentBuffer().Filename)
			}
			newPane := &Pane{Buffer: newBuf}
			e.Panes = append(e.Panes, newPane)
			e.SplitType = SplitVertical
			e.StatusMsg = "Vertical split"
		}
		
	case "close":
		if len(e.Panes) > 1 {
			e.Panes = append(e.Panes[:e.ActivePane], e.Panes[e.ActivePane+1:]...)
			if e.ActivePane >= len(e.Panes) {
				e.ActivePane = len(e.Panes) - 1
			}
			e.SplitType = SplitNone
		}
		
	default:
		e.StatusMsg = fmt.Sprintf("Unknown command: %s", cmd)
	}
}

func (e *Editor) InsertText(text string) {
	buf := e.CurrentBuffer()
	lines := strings.Split(text, "\n")
	
	if len(lines) == 1 {
		line := buf.Lines[buf.CursorY]
		buf.Lines[buf.CursorY] = line[:buf.CursorX] + text + line[buf.CursorX:]
		buf.CursorX += len(text)
	} else {
		currentLine := buf.Lines[buf.CursorY]
		beforeCursor := currentLine[:buf.CursorX]
		afterCursor := currentLine[buf.CursorX:]
		
		buf.Lines[buf.CursorY] = beforeCursor + lines[0]
		
		newLines := make([]string, 0, len(buf.Lines)+len(lines)-1)
		newLines = append(newLines, buf.Lines[:buf.CursorY+1]...)
		for i := 1; i < len(lines)-1; i++ {
			newLines = append(newLines, lines[i])
		}
		lastNewLine := lines[len(lines)-1] + afterCursor
		newLines = append(newLines, lastNewLine)
		newLines = append(newLines, buf.Lines[buf.CursorY+1:]...)
		
		buf.Lines = newLines
		buf.CursorY += len(lines) - 1
		buf.CursorX = len(lines[len(lines)-1])
	}
	buf.MarkDirty()
}

func (e *Editor) ScrollToCursor(pane *Pane) {
	buf := pane.Buffer
	
	if buf.CursorY < buf.OffsetY {
		buf.OffsetY = buf.CursorY
	} else if buf.CursorY >= buf.OffsetY+pane.Height {
		buf.OffsetY = buf.CursorY - pane.Height + 1
	}
	
	if buf.CursorX < buf.OffsetX {
		buf.OffsetX = buf.CursorX
	} else if buf.CursorX >= buf.OffsetX+pane.Width {
		buf.OffsetX = buf.CursorX - pane.Width + 1
	}
}

func (e *Editor) Run() {
	for {
		e.Draw()
		ev := e.Screen.PollEvent()
		if !e.HandleEvent(ev) {
			break
		}
	}
}

func main() {
	editor, err := NewEditor()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer editor.Screen.Fini()
	
	if len(os.Args) > 1 {
		if err := editor.CurrentBuffer().LoadFile(os.Args[1]); err != nil {
			editor.StatusMsg = fmt.Sprintf("Error loading file: %v", err)
		}
	}
	
	editor.Run()
}
