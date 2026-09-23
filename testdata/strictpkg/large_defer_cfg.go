package strictpkg

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/rs/zerolog"
)

type largeRecord struct{ a, b int }

func consumeLarge(*int) {}

// largeDeferCFG guards the judgement of deferred and concurrent sinks, which
// reads every state after their statement, against regressions to copying the
// whole frame at each of them. Nothing after the statements touches the event.
func largeDeferCFG(ctx context.Context, mu *sync.Mutex, data [64][]byte, flags [64]bool) {
	mu.Lock()
	defer mu.Unlock()
	logger := zerolog.New(io.Discard)
	event := logger.Info().Ctx(ctx)
	defer event.Msg("deferred sink sees every later state")
	go event.Msg("goroutine sees every later state")
	if flags[0] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[0], record)
		consumeLarge(&record.b)
	}
	if flags[1] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[1], record)
		consumeLarge(&record.b)
	}
	if flags[2] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[2], record)
		consumeLarge(&record.b)
	}
	if flags[3] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[3], record)
		consumeLarge(&record.b)
	}
	if flags[4] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[4], record)
		consumeLarge(&record.b)
	}
	if flags[5] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[5], record)
		consumeLarge(&record.b)
	}
	if flags[6] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[6], record)
		consumeLarge(&record.b)
	}
	if flags[7] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[7], record)
		consumeLarge(&record.b)
	}
	if flags[8] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[8], record)
		consumeLarge(&record.b)
	}
	if flags[9] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[9], record)
		consumeLarge(&record.b)
	}
	if flags[10] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[10], record)
		consumeLarge(&record.b)
	}
	if flags[11] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[11], record)
		consumeLarge(&record.b)
	}
	if flags[12] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[12], record)
		consumeLarge(&record.b)
	}
	if flags[13] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[13], record)
		consumeLarge(&record.b)
	}
	if flags[14] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[14], record)
		consumeLarge(&record.b)
	}
	if flags[15] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[15], record)
		consumeLarge(&record.b)
	}
	if flags[16] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[16], record)
		consumeLarge(&record.b)
	}
	if flags[17] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[17], record)
		consumeLarge(&record.b)
	}
	if flags[18] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[18], record)
		consumeLarge(&record.b)
	}
	if flags[19] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[19], record)
		consumeLarge(&record.b)
	}
	if flags[20] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[20], record)
		consumeLarge(&record.b)
	}
	if flags[21] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[21], record)
		consumeLarge(&record.b)
	}
	if flags[22] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[22], record)
		consumeLarge(&record.b)
	}
	if flags[23] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[23], record)
		consumeLarge(&record.b)
	}
	if flags[24] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[24], record)
		consumeLarge(&record.b)
	}
	if flags[25] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[25], record)
		consumeLarge(&record.b)
	}
	if flags[26] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[26], record)
		consumeLarge(&record.b)
	}
	if flags[27] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[27], record)
		consumeLarge(&record.b)
	}
	if flags[28] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[28], record)
		consumeLarge(&record.b)
	}
	if flags[29] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[29], record)
		consumeLarge(&record.b)
	}
	if flags[30] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[30], record)
		consumeLarge(&record.b)
	}
	if flags[31] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[31], record)
		consumeLarge(&record.b)
	}
	if flags[32] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[32], record)
		consumeLarge(&record.b)
	}
	if flags[33] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[33], record)
		consumeLarge(&record.b)
	}
	if flags[34] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[34], record)
		consumeLarge(&record.b)
	}
	if flags[35] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[35], record)
		consumeLarge(&record.b)
	}
	if flags[36] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[36], record)
		consumeLarge(&record.b)
	}
	if flags[37] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[37], record)
		consumeLarge(&record.b)
	}
	if flags[38] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[38], record)
		consumeLarge(&record.b)
	}
	if flags[39] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[39], record)
		consumeLarge(&record.b)
	}
	if flags[40] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[40], record)
		consumeLarge(&record.b)
	}
	if flags[41] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[41], record)
		consumeLarge(&record.b)
	}
	if flags[42] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[42], record)
		consumeLarge(&record.b)
	}
	if flags[43] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[43], record)
		consumeLarge(&record.b)
	}
	if flags[44] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[44], record)
		consumeLarge(&record.b)
	}
	if flags[45] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[45], record)
		consumeLarge(&record.b)
	}
	if flags[46] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[46], record)
		consumeLarge(&record.b)
	}
	if flags[47] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[47], record)
		consumeLarge(&record.b)
	}
	if flags[48] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[48], record)
		consumeLarge(&record.b)
	}
	if flags[49] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[49], record)
		consumeLarge(&record.b)
	}
	if flags[50] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[50], record)
		consumeLarge(&record.b)
	}
	if flags[51] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[51], record)
		consumeLarge(&record.b)
	}
	if flags[52] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[52], record)
		consumeLarge(&record.b)
	}
	if flags[53] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[53], record)
		consumeLarge(&record.b)
	}
	if flags[54] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[54], record)
		consumeLarge(&record.b)
	}
	if flags[55] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[55], record)
		consumeLarge(&record.b)
	}
	if flags[56] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[56], record)
		consumeLarge(&record.b)
	}
	if flags[57] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[57], record)
		consumeLarge(&record.b)
	}
	if flags[58] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[58], record)
		consumeLarge(&record.b)
	}
	if flags[59] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[59], record)
		consumeLarge(&record.b)
	}
	if flags[60] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[60], record)
		consumeLarge(&record.b)
	}
	if flags[61] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[61], record)
		consumeLarge(&record.b)
	}
	if flags[62] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[62], record)
		consumeLarge(&record.b)
	}
	if flags[63] {
		record := &largeRecord{}
		_ = json.Unmarshal(data[63], record)
		consumeLarge(&record.b)
	}
}
