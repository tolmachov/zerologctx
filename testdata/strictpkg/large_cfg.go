package strictpkg

import (
	"context"
	"io"

	"github.com/rs/zerolog"
)

// largeAliasCFG guards the worklist against regressions to repeated whole-tree
// scans. The pointer chain aliases one local memory location across 64 diamonds.
func largeAliasCFG(ctx context.Context, flags [64]bool) {
	plain := zerolog.New(io.Discard)
	contextual := plain.With().Ctx(ctx).Logger()
	logger := contextual
	p0 := &logger
	p1 := &*p0
	p2 := &*p1
	p3 := &*p2
	p4 := &*p3
	p5 := &*p4
	p6 := &*p5
	p7 := &*p6
	p8 := &*p7
	p9 := &*p8
	p10 := &*p9
	p11 := &*p10
	p12 := &*p11
	p13 := &*p12
	p14 := &*p13
	p15 := &*p14
	p16 := &*p15
	p17 := &*p16
	p18 := &*p17
	p19 := &*p18
	p20 := &*p19
	p21 := &*p20
	p22 := &*p21
	p23 := &*p22
	p24 := &*p23
	p25 := &*p24
	p26 := &*p25
	p27 := &*p26
	p28 := &*p27
	p29 := &*p28
	p30 := &*p29
	p31 := &*p30
	if flags[0] {
		*p31 = contextual
	}
	if flags[1] {
		*p31 = contextual
	}
	if flags[2] {
		*p31 = contextual
	}
	if flags[3] {
		*p31 = contextual
	}
	if flags[4] {
		*p31 = contextual
	}
	if flags[5] {
		*p31 = contextual
	}
	if flags[6] {
		*p31 = contextual
	}
	if flags[7] {
		*p31 = contextual
	}
	if flags[8] {
		*p31 = contextual
	}
	if flags[9] {
		*p31 = contextual
	}
	if flags[10] {
		*p31 = contextual
	}
	if flags[11] {
		*p31 = contextual
	}
	if flags[12] {
		*p31 = contextual
	}
	if flags[13] {
		*p31 = contextual
	}
	if flags[14] {
		*p31 = contextual
	}
	if flags[15] {
		*p31 = contextual
	}
	if flags[16] {
		*p31 = contextual
	}
	if flags[17] {
		*p31 = contextual
	}
	if flags[18] {
		*p31 = contextual
	}
	if flags[19] {
		*p31 = contextual
	}
	if flags[20] {
		*p31 = contextual
	}
	if flags[21] {
		*p31 = contextual
	}
	if flags[22] {
		*p31 = contextual
	}
	if flags[23] {
		*p31 = contextual
	}
	if flags[24] {
		*p31 = contextual
	}
	if flags[25] {
		*p31 = contextual
	}
	if flags[26] {
		*p31 = contextual
	}
	if flags[27] {
		*p31 = contextual
	}
	if flags[28] {
		*p31 = contextual
	}
	if flags[29] {
		*p31 = contextual
	}
	if flags[30] {
		*p31 = contextual
	}
	if flags[31] {
		*p31 = contextual
	}
	if flags[32] {
		*p31 = contextual
	}
	if flags[33] {
		*p31 = contextual
	}
	if flags[34] {
		*p31 = contextual
	}
	if flags[35] {
		*p31 = contextual
	}
	if flags[36] {
		*p31 = contextual
	}
	if flags[37] {
		*p31 = contextual
	}
	if flags[38] {
		*p31 = contextual
	}
	if flags[39] {
		*p31 = contextual
	}
	if flags[40] {
		*p31 = contextual
	}
	if flags[41] {
		*p31 = contextual
	}
	if flags[42] {
		*p31 = contextual
	}
	if flags[43] {
		*p31 = contextual
	}
	if flags[44] {
		*p31 = contextual
	}
	if flags[45] {
		*p31 = contextual
	}
	if flags[46] {
		*p31 = contextual
	}
	if flags[47] {
		*p31 = contextual
	}
	if flags[48] {
		*p31 = contextual
	}
	if flags[49] {
		*p31 = contextual
	}
	if flags[50] {
		*p31 = contextual
	}
	if flags[51] {
		*p31 = contextual
	}
	if flags[52] {
		*p31 = contextual
	}
	if flags[53] {
		*p31 = contextual
	}
	if flags[54] {
		*p31 = contextual
	}
	if flags[55] {
		*p31 = contextual
	}
	if flags[56] {
		*p31 = contextual
	}
	if flags[57] {
		*p31 = contextual
	}
	if flags[58] {
		*p31 = contextual
	}
	if flags[59] {
		*p31 = contextual
	}
	if flags[60] {
		*p31 = contextual
	}
	if flags[61] {
		*p31 = contextual
	}
	if flags[62] {
		*p31 = contextual
	}
	if flags[63] {
		*p31 = plain
	}
	logger.Info().Msg("large CFG may lose context") // want `zerolog output is not proven to carry context before Msg\(\)`
}
