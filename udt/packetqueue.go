package udt

import (
	"container/heap"

	"github.com/odysseus654/go-udt/udt/packet"
)

/*
A packetHeap is the internal implementation of a Heap used by packetQueue.
*/
type packetHeap []packet.Packet

func (h packetHeap) Len() int           { return len(h) }
func (h packetHeap) Less(i, j int) bool { return h[i].SendTime() < h[j].SendTime() }
func (h packetHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }

func (h *packetHeap) Push(x interface{}) {
	// Push and Pop use pointer receivers because they modify the slice's length,
	// not just its contents.
	*h = append(*h, x.(packet.Packet))
}

func (h *packetHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

/*
A packetQueue is a priority queue of packets sorted by ts.
*/
type packetQueue struct {
	h packetHeap
	l uint32
}

func (q *packetQueue) push(p packet.Packet) {
	heap.Push(&q.h, p)
	q.l++
}

func (q *packetQueue) peek() (p packet.Packet) {
	if q.l == 0 {
		return nil
	}
	return q.h[0]
}

func (q *packetQueue) pop() (p packet.Packet) {
	if q.l == 0 {
		return nil
	}
	q.l--
	return heap.Pop(&q.h).(packet.Packet)
}

func newPacketQueue() (q *packetQueue) {
	q = &packetQueue{}
	heap.Init(&q.h)
	return
}
