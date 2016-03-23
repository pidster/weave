package space

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/weaveworks/weave/common"
	"github.com/weaveworks/weave/net/address"
)

type Space struct {
	// ours and free represent a set of addresses as a sorted
	// sequences of ranges.  Even elements give the inclusive
	// starting points of ranges, and odd elements give the
	// exclusive ending points.  Ranges in an array do not
	// overlap, and neighbouring ranges are always coalesced if
	// possible, so the arrays consist of sorted Addrs without
	// repetition.
	ours []address.Address
	free []address.Address
}

func New() *Space {
	return &Space{}
}

func (s *Space) Add(start address.Address, size address.Offset) {
	s.free = add(s.free, start, address.Add(start, size))
}

// Clear removes all spaces from this space set.  Used during node shutdown.
func (s *Space) Clear() {
	s.ours = s.ours[:0]
	s.free = s.free[:0]
}

// Walk down the free list calling f() on the in-range portions, until
// f() returns true or we run out of free space.  Return true iff f() returned true
func (s *Space) walkFree(r address.Range, f func(address.Range) bool) bool {
	if r.Start >= r.End { // degenerate case
		return false
	}
	for i := 0; i < len(s.free); i += 2 {
		chunk := address.Range{Start: s.free[i], End: s.free[i+1]}
		if chunk.End <= r.Start { // this chunk comes before the range
			continue
		}
		if chunk.Start >= r.End {
			// all remaining free space is completely after range
			break
		}
		// at this point we know chunk.End>chunk.Start &&
		// chunk.End>r.Start && r.End>chunk.Start && r.End>r.Start
		// therefore max(start, r.Start) < min(end, r.End)
		// Restrict this block of free space to be in range
		if chunk.Start < r.Start {
			chunk.Start = r.Start
		}
		if chunk.End > r.End {
			chunk.End = r.End
		}
		// at this point we know start<end
		if f(chunk) {
			return true
		}
	}
	return false
}

func (s *Space) Allocate(r address.Range) (bool, address.Address) {
	var result address.Address
	return s.walkFree(r, func(chunk address.Range) bool {
		result = chunk.Start
		s.ours = add(s.ours, result, result+1)
		s.free = subtract(s.free, result, result+1)
		return true
	}), result
}

func (s *Space) Claim(addr address.Address) error {
	if !contains(s.free, addr) {
		return fmt.Errorf("Address %v is not free to claim", addr)
	}

	s.ours = add(s.ours, addr, addr+1)
	s.free = subtract(s.free, addr, addr+1)
	return nil
}

func (s *Space) NumFreeAddresses() address.Count {
	res := address.Count(0)
	for i := 0; i < len(s.free); i += 2 {
		res += address.Length(s.free[i+1], s.free[i])
	}
	return res
}

func (s *Space) NumFreeAddressesInRange(r address.Range) address.Count {
	res := address.Count(0)
	s.walkFree(r, func(chunk address.Range) bool {
		res += chunk.Size()
		return false
	})
	return res
}

// IsFree checks whether there are no claimed addresses within
// the given address range.
func (s *Space) IsFree(r address.Range) bool {
	return r.Size() == s.NumFreeAddressesInRange(r)
}

// IsFull checks whether there are any free addresses within
// the given address range.
func (s *Space) IsFull(r address.Range) bool {
	return s.NumFreeAddressesInRange(r) == 0
}

func (s *Space) Free(addr address.Address) error {
	if !contains(s.ours, addr) {
		return fmt.Errorf("Address %v is not ours", addr)
	}
	if contains(s.free, addr) {
		return fmt.Errorf("Address %v is already free", addr)
	}

	s.ours = subtract(s.ours, addr, addr+1)
	s.free = add(s.free, addr, addr+1)
	return nil
}

func (s *Space) biggestFreeRange(r address.Range) (biggest address.Range) {
	biggestSize := address.Count(0)
	s.walkFree(r, func(chunk address.Range) bool {
		if size := chunk.Size(); size >= biggestSize {
			biggest = chunk
			biggestSize = size
		}
		return false
	})
	return
}

func (s *Space) biggestCIDRFreeRange(r address.Range) (biggest address.CIDR) {
	biggestSize := address.Offset(0)
	s.walkFree(r, func(chunk address.Range) bool {
		for _, cidr := range chunk.CIDRs() {
			if size := cidr.Size(); size >= biggestSize {
				biggest = cidr
				biggestSize = size
			}
		}
		return false
	})
	return
}

func (s *Space) Donate(r address.Range, isCIDRAligned bool,
	ownedCIDRRanges func() []address.CIDR) (address.Range, bool) {

	var chunk address.Range
	var ok bool

	if !isCIDRAligned {
		chunk, ok = s.findNonCIDRDonation(r)
	} else {
		chunk, ok = s.findCIDRDonation(ownedCIDRRanges())
	}

	if ok {
		s.remove(chunk)
	}

	return chunk, ok
}

func (s *Space) findNonCIDRDonation(r address.Range) (address.Range, bool) {
	var biggest address.Range

	biggest = s.biggestFreeRange(r)

	if biggest.Size() == 0 {
		return address.Range{}, false
	}

	// Donate half of that biggest free range. Note size/2 rounds down, so
	// the resulting donation size rounds up, and in particular can't be empty.
	biggest.Start = address.Add(biggest.Start, address.Offset(biggest.Size()/2))

	return biggest, true
}

// findCIDRDonation tries to find a free CIDR (sub)range within the given list of
// ranges.
//
// When searching for ranges we tend to find such that it would require the
// least amount of splits. The strategy is a direct outcome of the AWS VPC
// limitation for number of route entries per table.
//
// The algorithm for search is the following:
//
// 1) Filter out non-free ranges from the cidrs parameter.
// 2) Return the second half of the biggest range.
// 3) If 1) step did not return, start halving the ranges.
// 4) Check whether the first or the second half is free.
// 4.1) True: Return it.
// 4.2) False: Append non-full halves to the list of the iteration.
func (s *Space) findCIDRDonation(cidrs []address.CIDR) (address.Range, bool) {
	var free []address.CIDR

	// Check whether there exists any free CIDR range.
	for _, cidr := range cidrs {
		if s.IsFree(cidr.Range()) {
			free = append(free, cidr)
		}
	}
	if len(free) > 0 {
		// Return the second half of the biggest range
		biggest := free[0]
		for _, cidr := range free[1:] {
			if biggest.Size() < cidr.Size() {
				biggest = cidr
			}
		}
		if _, second, ok := biggest.Halve(); ok {
			biggest = second
		}
		return biggest.Range(), true
	}

	// No free CIDR ranges, so let's start splitting them to find such.
	// Return once we found a suitable range.
	for len(cidrs) != 0 {
		var next []address.CIDR
		for _, cidr := range cidrs {
			first, second, ok := cidr.Halve()
			if !ok {
				if s.IsFree(cidr.Range()) {
					// This case is never reached, because free CIDRs of /32
					// are checked either in 1) step or in previous iterations
					// of the loop.
					panic("should not reach")
				}
				continue
			}
			if s.IsFree(first.Range()) {
				return first.Range(), true
			}
			if s.IsFree(second.Range()) {
				return second.Range(), true
			}
			if !s.IsFull(first.Range()) {
				next = append(next, first)
			}
			if !s.IsFull(second.Range()) {
				next = append(next, second)
			}
		}
		cidrs = next
	}

	return address.Range{}, false
}

// remove removes the given range from the space.
func (s *Space) remove(r address.Range) {
	s.ours = subtract(s.ours, r.Start, r.End)
	s.free = subtract(s.free, r.Start, r.End)
}

func firstGreater(a []address.Address, x address.Address) int {
	return sort.Search(len(a), func(i int) bool { return a[i] > x })
}

func firstGreaterOrEq(a []address.Address, x address.Address) int {
	return sort.Search(len(a), func(i int) bool { return a[i] >= x })
}

// Do the ranges contain the given address?
func contains(addrs []address.Address, addr address.Address) bool {
	return firstGreater(addrs, addr)&1 != 0
}

// Take the union of the range [start, end) with the ranges in the array
func add(addrs []address.Address, start address.Address, end address.Address) []address.Address {
	return addSub(addrs, start, end, 0)
}

// Subtract the range [start, end) from the ranges in the array
func subtract(addrs []address.Address, start address.Address, end address.Address) []address.Address {
	return addSub(addrs, start, end, 1)
}

func addSub(addrs []address.Address, start address.Address, end address.Address, sense int) []address.Address {
	startPos := firstGreaterOrEq(addrs, start)
	endPos := firstGreater(addrs[startPos:], end) + startPos

	// Boundaries up to startPos are unaffected
	res := make([]address.Address, startPos, len(addrs)+2)
	copy(res, addrs)

	// Include start and end as new boundaries if they lie
	// outside/inside existing ranges (according to sense).
	if startPos&1 == sense {
		res = append(res, start)
	}

	if endPos&1 == sense {
		res = append(res, end)
	}

	// Boundaries after endPos are unaffected
	return append(res, addrs[endPos:]...)
}

func (s *Space) String() string {
	var buf bytes.Buffer
	if len(s.ours) > 0 {
		fmt.Fprint(&buf, "owned:")
		for i := 0; i < len(s.ours); i += 2 {
			fmt.Fprintf(&buf, " %s+%d ", s.ours[i], s.ours[i+1]-s.ours[i])
		}
	}
	if len(s.free) > 0 {
		fmt.Fprintf(&buf, "free:")
		for i := 0; i < len(s.free); i += 2 {
			fmt.Fprintf(&buf, " %s+%d ", s.free[i], s.free[i+1]-s.free[i])
		}
	}
	if len(s.ours) == 0 && len(s.free) == 0 {
		fmt.Fprintf(&buf, "No address ranges owned")
	}
	return buf.String()
}

type addressSlice []address.Address

func (p addressSlice) Len() int           { return len(p) }
func (p addressSlice) Less(i, j int) bool { return p[i] < p[j] }
func (p addressSlice) Swap(i, j int)      { p[i], p[j] = p[j], p[i] }

func (s *Space) assertInvariants() {
	common.Assert(sort.IsSorted(addressSlice(s.ours)))
	common.Assert(sort.IsSorted(addressSlice(s.free)))
}

// Return a slice representing everything we own, whether it is free or not
func (s *Space) everything() []address.Address {
	a := make([]address.Address, len(s.ours))
	copy(a, s.ours)
	for i := 0; i < len(s.free); i += 2 {
		a = add(a, s.free[i], s.free[i+1])
	}
	return a
}

// OwnedRanges returns slice of Ranges, ordered by IP, gluing together
// contiguous sequences of owned and free addresses
func (s *Space) OwnedRanges() []address.Range {
	everything := s.everything()
	result := make([]address.Range, len(everything)/2)
	for i := 0; i < len(everything); i += 2 {
		result[i/2] = address.Range{Start: everything[i], End: everything[i+1]}
	}
	return result
}

// Create a Space that has free space in all the supplied Ranges.
func (s *Space) AddRanges(ranges []address.Range) {
	for _, r := range ranges {
		s.free = add(s.free, r.Start, r.End)
	}
}

// Taking ranges to be a set of all space we should own, add in any excess as free space
func (s *Space) UpdateRanges(ranges []address.Range) {
	new := []address.Address{}
	for _, r := range ranges {
		new = add(new, r.Start, r.End)
	}
	current := s.everything()
	for i := 0; i < len(current); i += 2 {
		new = subtract(new, current[i], current[i+1])
	}
	for i := 0; i < len(new); i += 2 {
		s.free = add(s.free, new[i], new[i+1])
	}
}
