package udt

import "github.com/odysseus654/go-udt/udt/packet"

// packetIdHeap defines a list of sorted packet IDs
type packetIDHeap []packet.PacketID

func (h packetIDHeap) Len() int {
	return len(h)
}

func (h packetIDHeap) Less(i, j int) bool {
	return h[i].Seq < h[j].Seq
}

func (h packetIDHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *packetIDHeap) Push(x interface{}) { // Push and Pop use pointer receivers because they modify the slice's length, not just its contents.
	*h = append(*h, x.(packet.PacketID))
}

func (h *packetIDHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}
