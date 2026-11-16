package rule

// Delta is one seat's chip change. The slice from Balance sums to zero.
type Delta struct {
	Seat  int `json:"Seat"`
	Delta int `json:"Delta"`
}

// Balance pays each loser -1 and splits that pot across winners.
// Remainder chips go to winners in ascending seat order.
// An empty winner list, or winners covering every seat, is a draw.
func Balance(seats []int, winners []int) []Delta {
	win := map[int]bool{}
	for _, seat := range winners {
		win[seat] = true
	}
	out := make([]Delta, 0, len(seats))
	losers := 0
	winnerSeats := make([]int, 0, len(winners))
	for _, seat := range seats {
		if win[seat] {
			winnerSeats = append(winnerSeats, seat)
			continue
		}
		losers++
	}
	if losers == 0 || len(winnerSeats) == 0 {
		for _, seat := range seats {
			out = append(out, Delta{Seat: seat})
		}
		return out
	}
	share := losers / len(winnerSeats)
	extra := losers % len(winnerSeats)
	// Lowest seats receive the remainder.
	order := append([]int(nil), winnerSeats...)
	for i := 0; i < len(order); i++ {
		for j := i + 1; j < len(order); j++ {
			if order[j] < order[i] {
				order[i], order[j] = order[j], order[i]
			}
		}
	}
	bonus := map[int]int{}
	for i := 0; i < extra; i++ {
		bonus[order[i]] = 1
	}
	for _, seat := range seats {
		if win[seat] {
			out = append(out, Delta{Seat: seat, Delta: share + bonus[seat]})
			continue
		}
		out = append(out, Delta{Seat: seat, Delta: -1})
	}
	return out
}
