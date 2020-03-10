package udt

import (
	"github.com/odysseus654/go-udt/udt/packet"
)

type NativeCongestionControl struct {
	startupComplete bool
	avgNAKNum       int // average number of NAKs in a congestion period
	nakCount        int // current number of NAKs in the current period
	decCount        int
	lastDecSeq      packet.PacketID // biggest sequence number when last time the packet sending rate is decreased
}

func (ncc NativeCongestionControl) Init(CongestionControlParms) {
	ncc.startupComplete = false
	ncc.avgNAKNum = 1
	ncc.nakCount = 1
	ncc.decCount = 1
	//ncc.lastDecSeq      packet.PacketID
}

func (ncc NativeCongestionControl) Close(CongestionControlParms) {

}

func (ncc NativeCongestionControl) OnACK(CongestionControlParms) {
	/*
			1) If the current status is in the slow start phase, set the
		      congestion window size to the product of packet arrival rate and
		      (RTT + SYN). Slow Start ends. Stop.
		   2) Set the congestion window size (CWND) to: CWND = A * (RTT + SYN) +
		      16.
		   3) The number of sent packets to be increased in the next SYN period
		      (inc) is calculated as:
		         if (B <= C)
		            inc = 1/PS;
		         else
		            inc = max(10^(ceil(log10((B-C)*PS*8))) * Beta/PS, 1/PS);
		      where B is the estimated link capacity and C is the current
		      sending speed. All are counted as packets per second. PS is the
		      fixed size of UDT packet counted in bytes. Beta is a constant
		      value of 0.0000015.
		   4) The SND period is updated as:
				 SND = (SND * SYN) / (SND * inc + SYN).*/
	/*
						 These four parameters are used in rate decrease, and their initial
		   values are in the parentheses: AvgNAKNum (1), NAKCount (1),
		   DecCount(1), LastDecSeq (initial sequence number - 1).*/
}

func (ncc NativeCongestionControl) OnNAK(CongestionControlParms) {
	/*
			We define a congestion period as the period between two NAKs in which
		   the first biggest lost packet sequence number is greater than the
		   LastDecSeq, which is the biggest sequence number when last time the
		   packet sending rate is decreased.

		   AvgNAKNum is the average number of NAKs in a congestion period.
		   NAKCount is the current number of NAKs in the current period.
	*/
	/*
	   1) If it is in slow start phase, set inter-packet interval to
	         1/recvrate. Slow start ends. Stop.
	      2) If this NAK starts a new congestion period, increase inter-packet
	         interval (snd) to snd = snd * 1.125; Update AvgNAKNum, reset
	         NAKCount to 1, and compute DecRandom to a random (average
	         distribution) number between 1 and AvgNAKNum. Update LastDecSeq.
	         Stop.
	      3) If DecCount <= 5, and NAKCount == DecCount * DecRandom:
	           a. Update SND period: SND = SND * 1.125;
	           b. Increase DecCount by 1;
	           c. Record the current largest sent sequence number (LastDecSeq).
	*/
}

func (ncc NativeCongestionControl) OnTimeout(CongestionControlParms) {

}

func (ncc NativeCongestionControl) OnPktSent(CongestionControlParms) {

}

func (ncc NativeCongestionControl) OnPktRecv(CongestionControlParms) {

}
