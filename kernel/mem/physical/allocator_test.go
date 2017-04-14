package physical

import (
	"strconv"
	"testing"
	"unsafe"

	"github.com/achilleasa/gopher-os/kernel/mem"
)

func TestUpdateHigherOrderFlagsForInvalidOrder(t *testing.T) {
	alloc, _ := testAllocator(1)
	alloc.updateHigherOrderFlags(0, MaxPageOrder)
	alloc.updateHigherOrderFlags(0, MaxPageOrder+1)
}

func TestUpdateHigherOrderFlags(t *testing.T) {
	memSizeMB := uint64(4)
	pageCount := memSizeMB * 1024 * 1024 >> mem.PageShift

	alloc, _ := testAllocator(memSizeMB)

	for page := uint64(0); page < pageCount; page++ {
		for _, bitmap := range alloc.freeBitmap {
			for i := 0; i < len(bitmap); i++ {
				bitmap[i] = 0
			}
		}

		// Set the ord(0) bit that corresponds to that page to 1 and check that all parents are marked as reserved
		block := page / 64
		blockMask := uint64(1 << (63 - (page % 64)))
		alloc.freeBitmap[0][block] |= blockMask
		alloc.updateHigherOrderFlags(uintptr(page<<mem.PageShift), 0)
		for bitIndex, ord := page, 0; ord < MaxPageOrder; bitIndex, ord = bitIndex>>1, ord+1 {
			val := alloc.freeBitmap[ord][bitIndex/64]
			valMask := uint64(1 << (63 - (bitIndex % 64)))
			if (val & valMask) == 0 {
				t.Errorf("[page %04d] expected [ord %d, block %d, bit %d] to be 1; got block value %064s", page, ord, bitIndex/64, 63-(bitIndex%64), strconv.FormatUint(val, 2))
			}
		}

		// Now clear the ord(0) bit and make sure that all parents are marked as free
		alloc.freeBitmap[0][block] ^= blockMask
		alloc.updateHigherOrderFlags(uintptr(page<<mem.PageShift), 0)
		for bitIndex, ord := page, 0; ord < MaxPageOrder; bitIndex, ord = bitIndex>>1, ord+1 {
			val := alloc.freeBitmap[ord][bitIndex/64]
			if val != 0 {
				t.Errorf("[page %04d] expected [ord %d, block %d, bit %d] to be 0; got block value %064s", page, ord, bitIndex/64, 63-(bitIndex%64), strconv.FormatUint(val, 2))
			}
		}

		// Check buddy pages for even pages
		if page%2 == 0 {
			// Set the ord(0) bit for the buddy page and check that all parents (starting at ord 1) are marked as reserved
			// same bits to be set for ord(1 to MaxPageOrder)
			alloc.freeBitmap[0][block] |= blockMask >> 1
			alloc.updateHigherOrderFlags(uintptr((page+1)<<mem.PageShift), 0)
			for bitIndex, ord := page>>1, 1; ord < MaxPageOrder; bitIndex, ord = bitIndex>>1, ord+1 {
				val := alloc.freeBitmap[ord][bitIndex/64]
				valMask := uint64(1 << (63 - (bitIndex % 64)))
				if (val & valMask) == 0 {
					t.Errorf("[page %04d] expected [ord %d, block %d, bit %d] to be 1; got block value %064s", page, ord, bitIndex/64, 63-(bitIndex%64), strconv.FormatUint(val, 2))
				}
			}

			// Now clear the ord(0) bit for the buddy page and make sure that all parents are marked as free
			alloc.freeBitmap[0][block] ^= blockMask >> 1
			alloc.updateHigherOrderFlags(uintptr((page+1)<<mem.PageShift), 0)
			for bitIndex, ord := page, 0; ord < MaxPageOrder; bitIndex, ord = bitIndex>>1, ord+1 {
				val := alloc.freeBitmap[ord][bitIndex/64]
				if val != 0 {
					t.Errorf("[page %04d] expected [ord %d, block %d, bit %d] to be 0; got block value %064s", page, ord, bitIndex/64, 63-(bitIndex%64), strconv.FormatUint(val, 2))
				}
			}

			// Finally mark both buddy pages at ord(0) as used and check that all parents (starting at ord 1) are marked as reserved
			alloc.freeBitmap[0][block] |= blockMask
			alloc.freeBitmap[0][block] |= blockMask >> 1
			alloc.updateHigherOrderFlags(uintptr(page<<mem.PageShift), 0)
			alloc.updateHigherOrderFlags(uintptr((page+1)<<mem.PageShift), 0)
			for bitIndex, ord := page>>1, 1; ord < MaxPageOrder; bitIndex, ord = bitIndex>>1, ord+1 {
				val := alloc.freeBitmap[ord][bitIndex/64]
				valMask := uint64(1 << (63 - (bitIndex % 64)))
				if (val & valMask) == 0 {
					t.Errorf("[page %04d] expected [ord %d, block %d, bit %d] to be 1; got block value %064s", page, ord, bitIndex/64, 63-(bitIndex%64), strconv.FormatUint(val, 2))
				}
			}
		}
	}
}

func TestSetBitmapSizes(t *testing.T) {
	specs := []struct {
		pages         uint64
		expBitmapSize [MaxPageOrder]int
	}{
		{
			1024, // 4mb
			[MaxPageOrder]int{16, 8, 4, 2, 1, 1, 1, 1, 1, 1},
		},
		{
			32 * 1024, // 128MB
			[MaxPageOrder]int{512, 256, 128, 64, 32, 16, 8, 4, 2, 1},
		},
		{
			1, // 4K
			// We need a full uint64 for ord(0) and we waste an empty
			// uint64 for each order due to rounding
			[MaxPageOrder]int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1},
		},
		{
			1025, // 4mb + 4k
			[MaxPageOrder]int{17, 9, 5, 3, 2, 1, 1, 1, 1, 1},
		},
	}

	for specIndex, spec := range specs {
		alloc := &buddyAllocator{}
		alloc.setBitmapSizes(spec.pages)

		for ord := 0; ord < MaxPageOrder; ord++ {
			if alloc.bitmapSlice[ord].Len != alloc.bitmapSlice[ord].Cap {
				t.Errorf("[spec %d] ord(%d): expected slice Len to be equal to the slice Cap; got %d, %d", specIndex, ord, alloc.bitmapSlice[ord].Len, alloc.bitmapSlice[ord].Cap)
			}

			if alloc.bitmapSlice[ord].Len != spec.expBitmapSize[ord] {
				t.Errorf("[spec %d] expected bitmap size for ord(%d) to be %d; got %d", specIndex, ord, spec.expBitmapSize[ord], alloc.bitmapSlice[ord].Len)
			}
		}
	}
}

func TestSetBitmapPointers(t *testing.T) {
	alloc, scratchBuf := testAllocator(4)
	for _, bitmap := range alloc.freeBitmap {
		for i := 0; i < len(bitmap); i++ {
			bitmap[i] = 0
		}
	}

	// Check that the entire scratchBuf has been erased
	for i := 0; i < len(scratchBuf); i++ {
		if got := scratchBuf[i]; got != 0 {
			t.Errorf("expected scratchBuf[%d] to be set to 0; got 0x%x", i, got)
		}
	}
}

func TestAlign(t *testing.T) {
	specs := []struct {
		in     uint64
		n      uint64
		expOut uint64
	}{
		{0, 64, 0},
		{1, 64, 64},
		{63, 64, 64},
		{64, 64, 64},
		{65, 64, 128},
	}

	for specIndex, spec := range specs {
		out := align(spec.in, spec.n)
		if out != spec.expOut {
			t.Errorf("[spec %d] expected align(%d, %d) to return %d; got %d", specIndex, spec.in, spec.n, spec.expOut, out)
		}
	}
}

func testAllocator(memInMB uint64) (*buddyAllocator, []byte) {
	alloc := &buddyAllocator{}
	alloc.setBitmapSizes(memInMB * 1024 * 1024 / mem.PageSize)

	requiredSize := 0
	for _, hdr := range alloc.bitmapSlice {
		requiredSize += hdr.Len * 8
	}

	// Allocate scratch buffer and set it to a known pattern
	scratchBuf := make([]byte, requiredSize)
	for i := 0; i < len(scratchBuf); i++ {
		scratchBuf[i] = 0xFF
	}

	// Setup pointers
	alloc.setBitmapPointers(uintptr(unsafe.Pointer(&scratchBuf[0])))
	return alloc, scratchBuf
}