package internal

type history struct {
	inputs []string

	pos int
}

func (h *history) Append(in string) {
	if len(h.inputs) == 0 || h.inputs[len(h.inputs)-1] != in {
		h.inputs = append(h.inputs, in)
	}
	h.pos = len(h.inputs) - 1
}

func (h *history) Next() string {
	last := len(h.inputs) - 1
	if last < 0 {
		return ""
	}
	if h.pos >= last {
		return h.inputs[last]
	}

	h.pos++
	return h.inputs[h.pos-1]
}

func (h *history) Prev() string {
	last := len(h.inputs) - 1
	if last < 0 {
		return ""
	}
	prompt := h.inputs[h.pos]

	if h.pos > 0 {
		h.pos--
	}
	return prompt
}


func NewHistory() *history {
	return &history{
		inputs: make([]string, 0),
		pos:    0,
	}
}
