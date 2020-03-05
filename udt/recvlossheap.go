package udt

import (
	"container/heap"
	"time"
)

type recvLossEntry struct {
	packetID     uint32
	lastFeedback time.Time
	numNAK       uint
}

// receiveLossList defines a list of recvLossEntry records sorted by their packet ID
type receiveLossHeap []recvLossEntry

func (h receiveLossHeap) Len() int {
	return len(h)
}

func (h receiveLossHeap) Less(i, j int) bool {
	return h[i].packetID < h[j].packetID
}

func (h receiveLossHeap) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
}

func (h *receiveLossHeap) Push(x interface{}) { // Push and Pop use pointer receivers because they modify the slice's length, not just its contents.
	*h = append(*h, x.(recvLossEntry))
}

func (h *receiveLossHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

// Min returns the smallest packet ID in this heap
func (h receiveLossHeap) Min() uint32 {
	len := len(h)
	idx := 0
	for {
		newIdx := idx * 2
		if newIdx >= len {
			return h[idx].packetID
		}
		idx = newIdx
	}
	return 0
}

// Max returns the largest packet ID in this heap
func (h receiveLossHeap) Max() uint32 {
	len := len(h)
	idx := 0
	for {
		newIdx := idx*2 + 1
		if newIdx >= len {
			return h[idx].packetID
		}
		idx = newIdx
	}
	return 0
}

func (h receiveLossHeap) sortedImpl(idx int, len int, iter func(recvLossEntry) bool) bool {
	next := idx * 2
	if next < len {
		if !h.sortedImpl(next, len, iter) {
			return false
		}
	}
	if !iter(h[idx]) {
		return false
	}

	next++
	if next < len {
		if !h.sortedImpl(next, len, iter) {
			return false
		}
	}

	return true
}

// Range calls the passed function in sorted order
func (h receiveLossHeap) Sorted(iter func(recvLossEntry) bool) {
	h.sortedImpl(0, len(h), iter)
}

// Find does a binary search of the heap for the specified packetID which is returned
func (h receiveLossHeap) Find(packetID uint32) *recvLossEntry {
	len := len(h)
	idx := 0
	for idx < len {
		pid := h[idx].packetID
		if pid == packetID {
			return &h[idx]
		} else if pid < packetID {
			idx = idx * 2
		} else {
			idx = idx*2 + 1
		}
	}
	return nil
}

// Remove does a binary search of the heap for the specified packetID, which is removed
func (h *receiveLossHeap) Remove(packetID uint32) bool {
	len := len(*h)
	idx := 0
	for idx < len {
		pid := (*h)[idx].packetID
		if pid == packetID {
			heap.Remove(h, idx)
			return true
		} else if pid < packetID {
			idx = idx * 2
		} else {
			idx = idx*2 + 1
		}
	}
	return false
}
