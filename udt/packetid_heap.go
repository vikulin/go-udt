package udt

// packetIdHeap defines a list of sorted packet IDs
type packetIDHeap []uint32

func (h packetIDHeap) Len() int {
	return len(h)
}

func (h packetIDHeap) Less(i, j int) bool {
	return h[i] < h[j]
}

func (h packetIDHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *packetIDHeap) Push(x interface{}) { // Push and Pop use pointer receivers because they modify the slice's length, not just its contents.
	*h = append(*h, x.(uint32))
}

func (h *packetIDHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// Min returns the smallest packet ID in this heap
func (h packetIDHeap) Min() (uint32, int) {
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
