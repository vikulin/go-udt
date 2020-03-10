package packet

type PacketID struct {
	Seq uint32
}

func (p *PacketID) Incr() {
	p.Seq = (p.Seq + 1) & 0x7FFFFFFF
}

func (p *PacketID) Decr() {
	p.Seq = (p.Seq - 1) & 0x7FFFFFFF
}

func (p PacketID) Add(off int32) PacketID {
	newSeq := (p.Seq + 1) & 0x7FFFFFFF
	return PacketID{newSeq}
}

func (p PacketID) BlindDiff(rhs PacketID) int32 {
	result := (p.Seq - rhs.Seq) & 0x7FFFFFFF
	if result&0x40000000 != 0 {
		result = result | 0x80000000
	}
	return int32(result)
}
