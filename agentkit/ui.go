package main

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// ── ANSI styling ─────────────────────────────────────────────────────────
// Kept minimal and dependency-free on purpose: this whole project only
// imports the Go standard library, so `go run .` works on a bare machine
// with zero `go get`.

const (
	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	italic  = "\033[3m"
	red     = "\033[31m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	blue    = "\033[34m"
	magenta = "\033[35m"
	cyan    = "\033[36m"
	gray    = "\033[90m"
)

func banner() string {
	art := cyan + bold + `
   ▄▄▄        ▄████ ▓█████  ███▄    █ ▄▄▄█████▓ ██ ▄█▀ ██▓▄▄▄█████▓
  ▒████▄     ██▒ ▀█▒▓█   ▀  ██ ▀█   █ ▓  ██▒ ▓▒ ██▄█▒ ▓██▒▓  ██▒ ▓▒
  ▒██  ▀█▄  ▒██░▄▄▄░▒███   ▓██  ▀█ ██▒▒ ▓██░ ▒░▓███▄░ ▒██▒▒ ▓██░ ▒░
  ░██▄▄▄▄██ ░▓█  ██▓▒▓█  ▄ ▓██▒  ▐▌██▒░ ▓██▓ ░ ▓██ █▄ ░██░░ ▓██▓ ░
   ▓█   ▓██▒░▒▓███▀▒░▒████▒▒██░   ▓██░  ▒██▒ ░ ▒██▒ █▄░██░  ▒██▒ ░
   ▒▒   ▓▒█░ ░▒   ▒ ░░ ▒░ ░░ ▒░   ▒ ▒   ▒ ░░   ▒ ▒▒ ▓▒░▓    ▒ ░░
    ▒   ▒▒ ░  ░   ░  ░ ░  ░░ ░░   ░ ▒░    ░    ░ ░▒ ▒░ ▒ ░    ░
    ░   ▒   ░ ░   ░    ░      ░   ░ ░   ░      ░ ░░ ░  ▒ ░  ░
        ░  ░      ░    ░  ░         ░          ░  ░    ░
` + reset
	sub := gray + italic + "  a tiny, dependency-free tool-calling agent loop for Go" + reset
	return art + "\n" + sub + "\n"
}

// spinner renders an animated "thinking" indicator on the current line
// until Stop() is called. It's intentionally cheap: one goroutine, one
// ticker, no external deps.
type spinner struct {
	frames []string
	label  string
	stopCh chan struct{}
	wg     sync.WaitGroup
	start  time.Time
}

func newSpinner(label string) *spinner {
	return &spinner{
		frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
		label:  label,
		stopCh: make(chan struct{}),
		start:  time.Now(),
	}
}

func (s *spinner) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-s.stopCh:
				return
			case <-ticker.C:
				frame := s.frames[i%len(s.frames)]
				fmt.Printf("\r%s%s%s %s%s", cyan, frame, reset, s.label, strings.Repeat(" ", 4))
				i++
			}
		}
	}()
}

// Stop halts the spinner and clears its line. If ok is true a green check
// is printed with the elapsed time; otherwise a red cross.
func (s *spinner) Stop(ok bool, finalLabel string) {
	close(s.stopCh)
	s.wg.Wait()
	elapsed := time.Since(s.start)
	icon := green + "✔" + reset
	if !ok {
		icon = red + "✘" + reset
	}
	fmt.Printf("\r%s %s %s(%s)%s%s\n", icon, finalLabel, gray, elapsed.Round(time.Millisecond), reset, strings.Repeat(" ", 6))
}

// box prints a lightweight rounded box around a block of text — used for
// the agent's final answer so it visually stands out in the trace.
func box(title, body string, color string) {
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	width := len(title)
	for _, l := range lines {
		if len(l) > width {
			width = len(l)
		}
	}
	if width > 88 {
		width = 88
	}
	top := color + "╭─ " + bold + title + reset + color + " " + strings.Repeat("─", max0(width-len(title)-1)) + "╮" + reset
	fmt.Println(top)
	for _, l := range wrapLines(lines, width) {
		pad := width - visibleLen(l)
		if pad < 0 {
			pad = 0
		}
		fmt.Printf("%s│%s %s%s %s│%s\n", color, reset, l, strings.Repeat(" ", pad), color, reset)
	}
	fmt.Println(color + "╰" + strings.Repeat("─", width+2) + "╯" + reset)
}

func wrapLines(lines []string, width int) []string {
	var out []string
	for _, l := range lines {
		for len(l) > width {
			out = append(out, l[:width])
			l = l[width:]
		}
		out = append(out, l)
	}
	return out
}

func visibleLen(s string) int { return len(s) } // plain ASCII/UTF-8 byte-ish estimate, good enough for our own strings

func max0(n int) int {
	if n < 0 {
		return 0
	}
	return n
}

func stepHeader(n int) string {
	return fmt.Sprintf("%s%s┌─ step %d ─────────────────────────────────────%s", gray, bold, n, reset)
}
