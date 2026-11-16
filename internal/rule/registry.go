package rule

import "sync"

var (
	regMu sync.RWMutex
	regs  = map[string]Rule{}
)

// Register is called from a plugin init. Duplicate codes panic at startup.
func Register(r Rule) {
	if r == nil || r.Code() == "" {
		panic("rule code is empty")
	}
	regMu.Lock()
	defer regMu.Unlock()
	if _, ok := regs[r.Code()]; ok {
		panic("duplicate rule " + r.Code())
	}
	regs[r.Code()] = r
}

func Get(code string) (Rule, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	r, ok := regs[code]
	return r, ok
}

func Codes() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(regs))
	for code := range regs {
		out = append(out, code)
	}
	return out
}
