package udt

// sendLossHeap defines a list of recvLossEntry records sorted by their packet ID
type sendLossHeap []uint32

func (h sendLossHeap) Len() int {
	return len(h)
}

func (h sendLossHeap) Less(i, j int) bool {
	return h[i] < h[j]
}

func (h sendLossHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *sendLossHeap) Push(x interface{}) { // Push and Pop use pointer receivers because they modify the slice's length, not just its contents.
	*h = append(*h, x.(uint32))
}

func (h *sendLossHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// Min returns the smallest packet ID in this heap
func (h sendLossHeap) Min() (uint32, int) {
	len := len(h)
	idx := 0
	for {
		newIdx := idx * 2
		if newIdx >= len {
			return h[idx], idx
		}
		idx = newIdx
	}
	return 0, -1
}
