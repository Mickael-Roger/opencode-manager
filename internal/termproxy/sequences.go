package termproxy

import (
	"strconv"
	"strings"
	"sync"
)

// parseCSI parses the control sequence at the start of p, which begins with
// ESC [. It returns the parameter and intermediate bytes, the final byte, and the
// sequence length; ok is false when p holds no complete sequence.
func parseCSI(p []byte) (params string, final byte, n int, ok bool) {
	if len(p) < 3 || p[0] != 0x1b || p[1] != '[' {
		return "", 0, 0, false
	}
	for i := 2; i < len(p); i++ {
		switch b := p[i]; {
		case b >= 0x20 && b <= 0x3f:
		case b >= 0x40 && b <= 0x7e:
			return string(p[2:i]), b, i + 1, true
		default:
			return "", 0, 0, false
		}
	}
	return "", 0, 0, false
}

// detachKeyTranslator rewrites Ctrl+key, as encoded by the kitty keyboard
// protocol (CSI key;mods u) or xterm modifyOtherKeys (CSI 27;mods;key ~), to its
// legacy control byte. Programs such as Claude Code switch the terminal into
// those modes, where dtach, which only matches the raw byte, never sees its
// detach key. A sequence split across reads is held back until the next read
// completes it, or until Flush when no more input follows.
type detachKeyTranslator struct {
	key     byte
	pending []byte
}

// Translate returns the input to forward for p, keeping back an unterminated
// control sequence at its end.
func (t *detachKeyTranslator) Translate(p []byte) []byte {
	if len(t.pending) > 0 {
		p = append(t.pending, p...)
		t.pending = nil
	}
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); {
		if params, final, n, ok := parseCSI(p[i:]); ok {
			if isCtrlKey(params, final, t.key) {
				out = append(out, t.key&0x1f)
			} else {
				out = append(out, p[i:i+n]...)
			}
			i += n
			continue
		}
		if p[i] == 0x1b && csiPrefix(p[i:]) {
			t.pending = append([]byte(nil), p[i:]...)
			break
		}
		out = append(out, p[i])
		i++
	}
	return out
}

// Pending reports whether input is held back waiting for the rest of a
// sequence.
func (t *detachKeyTranslator) Pending() bool { return len(t.pending) > 0 }

// Flush returns the held back input unchanged, for when the rest of the
// sequence never comes, such as a lone Escape key press.
func (t *detachKeyTranslator) Flush() []byte {
	p := t.pending
	t.pending = nil
	return p
}

func isCtrlKey(params string, final, key byte) bool {
	code := strconv.Itoa(int(key))
	fields := strings.Split(params, ";")
	switch final {
	case 'u':
		// key[:shifted[:base]] ; mods[:event] [; text]. Accept press and repeat
		// events; a release must not detach a second time.
		if len(fields) < 2 || strings.SplitN(fields[0], ":", 2)[0] != code {
			return false
		}
		mods := strings.SplitN(fields[1], ":", 2)
		if len(mods) == 2 && mods[1] != "1" && mods[1] != "2" {
			return false
		}
		return ctrlOnly(mods[0])
	case '~':
		return len(fields) == 3 && fields[0] == "27" && fields[2] == code && ctrlOnly(fields[1])
	}
	return false
}

// ctrlOnly reports whether an encoded modifier value means Ctrl alone, ignoring
// the Caps Lock and Num Lock states the kitty protocol also reports.
func ctrlOnly(value string) bool {
	m, err := strconv.Atoi(value)
	if err != nil || m < 1 {
		return false
	}
	const ctrl, capsLock, numLock = 4, 64, 128
	return (m-1)&^(capsLock|numLock) == ctrl
}

// privateModes are the DEC private modes Modes tracks, in replay order: the
// alternate screen first, so the others apply to the screen the program drew
// on. The value is the mode's default state.
var privateModes = []struct {
	mode string
	on   bool
}{
	{"1049", false}, // alternate screen
	{"1000", false}, // mouse clicks
	{"1002", false}, // mouse drags
	{"1003", false}, // all mouse motion
	{"1006", false}, // SGR mouse encoding
	{"1004", false}, // focus events
	{"2004", false}, // bracketed paste
	{"2031", false}, // color scheme updates
	{"25", true},    // cursor visible
}

// Modes records the keyboard and reporting modes a program switched the host
// terminal into, as seen in its output. Detaching leaves the program running
// with the terminal still in those modes, so Reset undoes them for whatever
// uses the terminal next, and Restore puts them back when the user reattaches,
// since the program does not renegotiate them for a terminal it thinks it
// already configured. Keep one per session across attaches.
type Modes struct {
	mu         sync.Mutex
	kittyDepth int    // kitty keyboard flags pushed on the stack
	kittyFlags string // flags of the last push
	kittyMain  string // flags set outside the stack (CSI = flags u), "" when unset
	otherKeys  string // modifyOtherKeys level, "" when unset
	private    map[string]bool
	partial    []byte
}

// Observe scans program output for mode changes. A sequence split across
// calls is completed by the next one.
func (m *Modes) Observe(p []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.partial) > 0 {
		p = append(m.partial, p...)
		m.partial = nil
	}
	for i := 0; i < len(p); i++ {
		if p[i] != 0x1b {
			continue
		}
		params, final, n, ok := parseCSI(p[i:])
		if !ok {
			// An unterminated sequence at the end of the chunk is completed by the
			// next one; anything longer is not a mode change worth tracking.
			if tail := p[i:]; csiPrefix(tail) {
				m.partial = append([]byte(nil), tail...)
				return
			}
			continue
		}
		m.apply(params, final)
		i += n - 1
	}
}

// csiPrefix reports whether tail, which starts with ESC, is the start of a
// control sequence that a later read may complete. Anything longer than a
// sequence worth matching is not held back.
func csiPrefix(tail []byte) bool {
	if len(tail) >= 32 || len(tail) >= 2 && tail[1] != '[' {
		return false
	}
	for _, b := range tail[min(2, len(tail)):] {
		if b < 0x20 || b > 0x3f {
			return false
		}
	}
	return true
}

func (m *Modes) apply(params string, final byte) {
	switch {
	case final == 'u' && strings.HasPrefix(params, ">"):
		m.kittyDepth++
		m.kittyFlags = strings.TrimPrefix(params, ">")
	case final == 'u' && strings.HasPrefix(params, "<"):
		count, err := strconv.Atoi(strings.TrimPrefix(params, "<"))
		if err != nil || count < 1 {
			count = 1
		}
		m.kittyDepth = max(0, m.kittyDepth-count)
	case final == 'u' && strings.HasPrefix(params, "="):
		flags := strings.SplitN(strings.TrimPrefix(params, "="), ";", 2)[0]
		if m.kittyDepth > 0 {
			m.kittyFlags = flags
		} else {
			m.kittyMain = flags
		}
	case final == 'm' && (params == ">4" || strings.HasPrefix(params, ">4;")):
		m.otherKeys = strings.TrimPrefix(strings.TrimPrefix(params, ">4"), ";")
	case (final == 'h' || final == 'l') && strings.HasPrefix(params, "?"):
		for _, mode := range strings.Split(strings.TrimPrefix(params, "?"), ";") {
			for _, tracked := range privateModes {
				if tracked.mode == mode {
					if m.private == nil {
						m.private = make(map[string]bool)
					}
					m.private[mode] = final == 'h'
				}
			}
		}
	}
}

// Reset returns the sequences that return the terminal to its default modes.
// The recorded state is kept for Restore.
func (m *Modes) Reset() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	if m.kittyDepth > 0 {
		b.WriteString("\x1b[<" + strconv.Itoa(m.kittyDepth) + "u")
	}
	if m.kittyMain != "" && m.kittyMain != "0" {
		b.WriteString("\x1b[=0u")
	}
	if m.otherKeys != "" && m.otherKeys != "0" {
		b.WriteString("\x1b[>4;0m")
	}
	// Leave the alternate screen last, so the other resets apply to it.
	for i := len(privateModes) - 1; i >= 0; i-- {
		tracked := privateModes[i]
		if on, seen := m.private[tracked.mode]; seen && on != tracked.on {
			b.WriteString(privateMode(tracked.mode, tracked.on))
		}
	}
	return []byte(b.String())
}

// Restore returns the sequences that put the terminal back into the recorded
// modes, and records the terminal as holding exactly those.
func (m *Modes) Restore() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	for _, tracked := range privateModes {
		if on, seen := m.private[tracked.mode]; seen && on != tracked.on {
			b.WriteString(privateMode(tracked.mode, on))
		}
	}
	if m.kittyMain != "" && m.kittyMain != "0" {
		b.WriteString("\x1b[=" + m.kittyMain + "u")
	}
	if m.kittyDepth > 0 {
		b.WriteString("\x1b[>" + m.kittyFlags + "u")
		m.kittyDepth = 1
	}
	if m.otherKeys != "" && m.otherKeys != "0" {
		b.WriteString("\x1b[>4;" + m.otherKeys + "m")
	}
	return []byte(b.String())
}

func privateMode(mode string, on bool) string {
	if on {
		return "\x1b[?" + mode + "h"
	}
	return "\x1b[?" + mode + "l"
}
