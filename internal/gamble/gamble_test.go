package gamble

import (
	"testing"

	"github.com/bwmarrin/discordgo"
)

// setupGame resets global game state and disables the database for unit tests.
func setupGame() {
	database = nil
	GameState = game{
		Rounds:     []round{},
		BetOptions: []Player{},
		Players:    []Player{},
	}
}

// makePlayer constructs a Player with the given Discord user ID and username.
func makePlayer(id, name string) Player {
	return Player{
		User: &discordgo.User{
			ID:          id,
			Username:    name,
			GlobalName:  name,
		},
	}
}

// TestPlayerID verifies that Player.ID() returns the underlying Discord user ID.
func TestPlayerID(t *testing.T) {
	p := makePlayer("abc123", "Alice")
	if got := p.ID(); got != "abc123" {
		t.Errorf("Player.ID() = %q, want %q", got, "abc123")
	}
}

// TestAddWheelOption verifies that players are added without duplicates.
func TestAddWheelOption(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)
	GameState.AddWheelOption(alice) // duplicate

	if len(GameState.BetOptions) != 2 {
		t.Errorf("BetOptions len = %d, want 2", len(GameState.BetOptions))
	}
}

// TestRemoveWheelOption verifies that a player can be removed from options.
func TestRemoveWheelOption(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)
	GameState.RemoveWheelOption(alice)

	if len(GameState.BetOptions) != 1 {
		t.Errorf("BetOptions len = %d, want 1 after remove", len(GameState.BetOptions))
	}
	if GameState.BetOptions[0].ID() != bob.ID() {
		t.Errorf("remaining player = %q, want Bob", GameState.BetOptions[0].ID())
	}
}

// TestRemoveWheelOptionNotPresent verifies removing a non-existent player is a no-op.
func TestRemoveWheelOptionNotPresent(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	GameState.AddWheelOption(alice)
	GameState.RemoveWheelOption(makePlayer("99", "Ghost"))

	if len(GameState.BetOptions) != 1 {
		t.Errorf("BetOptions len = %d, want 1 (unmodified)", len(GameState.BetOptions))
	}
}

// TestAddPlayer verifies that players are added without duplicates.
func TestAddPlayer(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")

	GameState.AddPlayer(alice)
	GameState.AddPlayer(alice) // duplicate

	if len(GameState.Players) != 1 {
		t.Errorf("Players len = %d, want 1", len(GameState.Players))
	}
}

// TestAddRound verifies rounds are appended with correct 0-indexed IDs.
func TestAddRound(t *testing.T) {
	setupGame()

	GameState.AddRound()
	GameState.AddRound()

	if len(GameState.Rounds) != 2 {
		t.Errorf("Rounds len = %d, want 2", len(GameState.Rounds))
	}
	if GameState.Rounds[0].ID != 0 {
		t.Errorf("Rounds[0].ID = %d, want 0", GameState.Rounds[0].ID)
	}
	if GameState.Rounds[1].ID != 1 {
		t.Errorf("Rounds[1].ID = %d, want 1", GameState.Rounds[1].ID)
	}
}

// TestTotalRounds verifies TotalRounds returns the correct count.
func TestTotalRounds(t *testing.T) {
	setupGame()
	if GameState.TotalRounds() != 0 {
		t.Errorf("TotalRounds() = %d, want 0", GameState.TotalRounds())
	}

	GameState.AddRound()
	GameState.AddRound()
	GameState.AddRound()

	if GameState.TotalRounds() != 3 {
		t.Errorf("TotalRounds() = %d, want 3", GameState.TotalRounds())
	}
}

// TestCurrentRound verifies CurrentRound returns the last round or empty.
func TestCurrentRound(t *testing.T) {
	setupGame()

	r := GameState.CurrentRound()
	if r.ID != 0 {
		t.Errorf("CurrentRound() on empty game ID = %d, want 0", r.ID)
	}

	GameState.AddRound()
	GameState.AddRound()

	cur := GameState.CurrentRound()
	if cur.ID != 1 {
		t.Errorf("CurrentRound().ID = %d, want 1", cur.ID)
	}
}

// TestRoundIndexing verifies Round() returns correct rounds by 1-based index.
func TestRoundIndexing(t *testing.T) {
	setupGame()
	GameState.AddRound()
	GameState.AddRound()
	GameState.AddRound()

	// 1-indexed access
	if GameState.Round(1).ID != 0 {
		t.Errorf("Round(1).ID = %d, want 0", GameState.Round(1).ID)
	}
	if GameState.Round(3).ID != 2 {
		t.Errorf("Round(3).ID = %d, want 2", GameState.Round(3).ID)
	}

	// Out-of-bounds falls back to CurrentRound
	outOfRange := GameState.Round(99)
	if outOfRange.ID != GameState.CurrentRound().ID {
		t.Errorf("Round(99) = %d, want CurrentRound %d", outOfRange.ID, GameState.CurrentRound().ID)
	}

	// Zero falls back to CurrentRound
	zeroRound := GameState.Round(0)
	if zeroRound.ID != GameState.CurrentRound().ID {
		t.Errorf("Round(0) = %d, want CurrentRound %d", zeroRound.ID, GameState.CurrentRound().ID)
	}
}

// TestResetWheel verifies that ResetWheel clears all game state.
func TestResetWheel(t *testing.T) {
	setupGame()
	GameState.AddRound()
	GameState.AddWheelOption(makePlayer("1", "Alice"))
	GameState.AddPlayer(makePlayer("2", "Bob"))

	GameState.ResetWheel()

	if len(GameState.Rounds) != 0 {
		t.Errorf("after reset Rounds len = %d, want 0", len(GameState.Rounds))
	}
	if len(GameState.BetOptions) != 0 {
		t.Errorf("after reset BetOptions len = %d, want 0", len(GameState.BetOptions))
	}
	if len(GameState.Players) != 0 {
		t.Errorf("after reset Players len = %d, want 0", len(GameState.Players))
	}
}

// TestCurrentWheelOptions verifies that won players are excluded from current options.
func TestCurrentWheelOptions(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")
	charlie := makePlayer("3", "Charlie")

	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)
	GameState.AddWheelOption(charlie)

	// Round 0: Alice wins
	GameState.AddRound()
	GameState.Rounds[0].SetWinner(alice)

	opts := GameState.CurrentWheelOptions()
	if len(opts) != 2 {
		t.Errorf("CurrentWheelOptions() len = %d, want 2 (Alice excluded)", len(opts))
	}
	for _, opt := range opts {
		if opt.ID() == alice.ID() {
			t.Error("CurrentWheelOptions() should not include Alice (already won)")
		}
	}
}

// TestRoundHasWinner verifies HasWinner checks the Winner field.
func TestRoundHasWinner(t *testing.T) {
	r := round{}
	if r.HasWinner() {
		t.Error("HasWinner() = true for empty round, want false")
	}

	r.Winner = makePlayer("1", "Alice")
	if !r.HasWinner() {
		t.Error("HasWinner() = false after setting winner, want true")
	}
}

// TestRoundAddBetAndHasBet verifies bet insertion and lookup.
func TestRoundAddBetAndHasBet(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	r := &round{ID: 0}
	bet := Bet{Amount: 50, By: alice, On: bob}

	r.AddBet(bet)

	found, ok := r.HasBet(bet)
	if !ok {
		t.Fatal("HasBet() = false, expected to find the bet")
	}
	if found.Amount != 50 {
		t.Errorf("HasBet() amount = %d, want 50", found.Amount)
	}
}

// TestRoundAddBetUpdatesExisting verifies that placing a bet on the same pair updates amount.
func TestRoundAddBetUpdatesExisting(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	r := &round{ID: 0}
	r.AddBet(Bet{Amount: 50, By: alice, On: bob})
	r.AddBet(Bet{Amount: 100, By: alice, On: bob}) // update

	if len(r.Bets) != 1 {
		t.Errorf("expected 1 bet after update, got %d", len(r.Bets))
	}
	if r.Bets[0].Amount != 100 {
		t.Errorf("bet amount after update = %d, want 100", r.Bets[0].Amount)
	}
}

// TestRoundRemoveBet verifies that a bet can be removed.
func TestRoundRemoveBet(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")
	charlie := makePlayer("3", "Charlie")

	r := &round{ID: 0}
	r.AddBet(Bet{Amount: 50, By: alice, On: bob})
	r.AddBet(Bet{Amount: 30, By: alice, On: charlie})
	r.RemoveBet(alice, bob)

	if len(r.Bets) != 1 {
		t.Errorf("expected 1 bet after remove, got %d", len(r.Bets))
	}
	if r.Bets[0].On.ID() != charlie.ID() {
		t.Errorf("remaining bet is on %q, want charlie", r.Bets[0].On.ID())
	}
}

// TestRoundAddClaim verifies that claims are added without duplicates.
func TestRoundAddClaim(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")

	r := &round{ID: 0}
	r.AddClaim(alice)
	r.AddClaim(alice) // duplicate

	if len(r.Claims) != 1 {
		t.Errorf("Claims len = %d after duplicate add, want 1", len(r.Claims))
	}
}

// TestPlayerBets verifies PlayerBets counts and sums correctly.
func TestPlayerBets(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")
	charlie := makePlayer("3", "Charlie")

	r := round{
		ID: 0,
		Bets: []Bet{
			{Amount: 50, By: alice, On: bob},
			{Amount: 30, By: alice, On: charlie},
			{Amount: 100, By: bob, On: alice},
		},
	}

	count, total := GameState.PlayerBets(alice, r)
	if count != 2 {
		t.Errorf("PlayerBets() count = %d, want 2", count)
	}
	if total != 80 {
		t.Errorf("PlayerBets() total = %d, want 80", total)
	}

	countBob, totalBob := GameState.PlayerBets(bob, r)
	if countBob != 1 {
		t.Errorf("PlayerBets() Bob count = %d, want 1", countBob)
	}
	if totalBob != 100 {
		t.Errorf("PlayerBets() Bob total = %d, want 100", totalBob)
	}
}

// TestPayoutWin verifies that winning a bet returns (amount * (options - 1)).
func TestPayoutWin(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")
	charlie := makePlayer("3", "Charlie")

	// Set up wheel options: 3 players
	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)
	GameState.AddWheelOption(charlie)

	// Round 0: Bob wins; Alice bet 50 on Bob
	GameState.AddRound()
	GameState.Rounds[0].Bets = []Bet{
		{Amount: 50, By: alice, On: bob},
	}
	GameState.Rounds[0].SetWinner(bob)

	// With 3 options, multiplier = 3-1 = 2
	payout := GameState.payout(alice, GameState.Rounds[0])
	if payout != 100 { // 50 * 2
		t.Errorf("payout() for win = %d, want 100", payout)
	}
}

// TestPayoutLoss verifies that losing a bet deducts the bet amount.
func TestPayoutLoss(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")
	charlie := makePlayer("3", "Charlie")

	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)
	GameState.AddWheelOption(charlie)

	// Round 0: Charlie wins; Alice bet 50 on Bob (wrong)
	GameState.AddRound()
	GameState.Rounds[0].Bets = []Bet{
		{Amount: 50, By: alice, On: bob},
	}
	GameState.Rounds[0].SetWinner(charlie)

	payout := GameState.payout(alice, GameState.Rounds[0])
	if payout != -50 {
		t.Errorf("payout() for loss = %d, want -50", payout)
	}
}

// TestPayoutNoWinner verifies zero payout when no winner is set.
func TestPayoutNoWinner(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	GameState.AddRound()
	GameState.Rounds[0].Bets = []Bet{
		{Amount: 50, By: alice, On: bob},
	}
	// No winner set

	payout := GameState.payout(alice, GameState.Rounds[0])
	if payout != 0 {
		t.Errorf("payout() with no winner = %d, want 0", payout)
	}
}

// TestPlayerMoneyClaimsOnly verifies money accumulation from claims alone.
func TestPlayerMoneyClaimsOnly(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")

	GameState.AddPlayer(alice)
	GameState.AddRound()

	// Alice claims in round 0 (earns 100)
	GameState.Rounds[0].AddClaim(alice)

	money := GameState.playerMoney(alice, GameState.Rounds[0])
	if money != 100 {
		t.Errorf("playerMoney() after 1 claim = %d, want 100", money)
	}
}

// TestPlayerMoneyWithWinnings verifies money includes winnings from past rounds.
func TestPlayerMoneyWithWinnings(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")
	charlie := makePlayer("3", "Charlie")

	GameState.AddPlayer(alice)
	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)
	GameState.AddWheelOption(charlie)

	// Round 0: Alice claims 100, bets 50 on Bob (10%+ of 100), Bob wins → payout = 50*(3-1)=100
	GameState.AddRound()
	GameState.Rounds[0].AddClaim(alice)
	GameState.Rounds[0].Bets = []Bet{
		{Amount: 50, By: alice, On: bob},
	}
	GameState.Rounds[0].SetWinner(bob)

	// Round 1: check Alice's money
	GameState.AddRound()
	GameState.Rounds[1].AddClaim(alice)

	money := GameState.playerMoney(alice, GameState.Rounds[1])
	// After round 0: 100 (claim) + 100 (payout) = 200
	// Round 1: +100 (claim) = 300
	if money != 300 {
		t.Errorf("playerMoney() after win = %d, want 300", money)
	}
}

// TestPlayerMoneyTaxApplied verifies the tax is applied when bet% < 10%.
func TestPlayerMoneyTaxApplied(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")
	charlie := makePlayer("3", "Charlie")

	GameState.AddPlayer(alice)
	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)
	GameState.AddWheelOption(charlie)

	// Round 0: Alice claims 100 but bets NOTHING (0% < 10% threshold)
	GameState.AddRound()
	GameState.Rounds[0].AddClaim(alice)
	GameState.Rounds[0].SetWinner(bob) // Alice loses her nothing

	// Round 1: check Alice's money includes tax
	GameState.AddRound()
	GameState.Rounds[1].AddClaim(alice)

	// After round 0: alice has 100, bet% = 0%, taxPercentage = 10
	// tax = 100 * 3 * 10 / 100 = 30 → money = 70
	// round 1 claim: +100 → 170
	money := GameState.playerMoney(alice, GameState.Rounds[1])
	if money != 170 {
		t.Errorf("playerMoney() with tax = %d, want 170", money)
	}
}

// TestPlayerUsableMoney verifies that current-round bets are subtracted.
func TestPlayerUsableMoney(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")
	charlie := makePlayer("3", "Charlie")

	GameState.AddPlayer(alice)
	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)
	GameState.AddWheelOption(charlie)

	// Round 0: Alice has 100, bets 30 on Bob
	GameState.AddRound()
	GameState.Rounds[0].AddClaim(alice)
	// Directly mutate the current round bets via pointer
	GameState.Rounds[0].Bets = append(GameState.Rounds[0].Bets, Bet{Amount: 30, By: alice, On: bob})

	usable := GameState.PlayerUsableMoney(alice)
	if usable != 70 { // 100 - 30
		t.Errorf("PlayerUsableMoney() = %d, want 70", usable)
	}
}

// TestUnderThresholdPlayers verifies identification of players below the 10% bet threshold.
func TestUnderThresholdPlayers(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")
	charlie := makePlayer("3", "Charlie")

	GameState.AddPlayer(alice)
	GameState.AddPlayer(bob)
	GameState.AddPlayer(charlie)

	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)
	GameState.AddWheelOption(charlie)

	GameState.AddRound()
	// All players claim 100
	GameState.Rounds[0].AddClaim(alice)
	GameState.Rounds[0].AddClaim(bob)
	GameState.Rounds[0].AddClaim(charlie)

	// Bob bets 15 on Alice (15% of 100) → above threshold
	GameState.Rounds[0].Bets = []Bet{
		{Amount: 15, By: bob, On: alice},
	}

	// Alice and Charlie have 0% → under threshold; Bob has 15% → above threshold
	under := GameState.underThresholdPlayers(GameState.Rounds[0])
	if len(under) != 2 {
		t.Errorf("underThresholdPlayers() len = %d, want 2", len(under))
	}
	for _, p := range under {
		if p.ID() == bob.ID() {
			t.Error("Bob should NOT be in underThresholdPlayers (bet% >= 10)")
		}
	}
}

// TestRoundOutcome verifies round outcome reporting.
func TestRoundOutcome(t *testing.T) {
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	r := round{
		ID:     0,
		Winner: bob,
		Bets: []Bet{
			{Amount: 50, By: alice, On: bob},  // win
			{Amount: 30, By: bob, On: alice},  // loss
		},
	}

	outcomes := r.roundOutcome()
	if len(outcomes) != 2 {
		t.Fatalf("roundOutcome() len = %d, want 2", len(outcomes))
	}

	// First bet: Alice bet on Bob (winner) → won
	if !outcomes[0].won {
		t.Error("outcome[0].won = false, want true (Alice bet on winner)")
	}
	// Second bet: Bob bet on Alice (not winner) → lost
	if outcomes[1].won {
		t.Error("outcome[1].won = true, want false (Bob bet on loser)")
	}
}

// TestRoundOutcomeNoWinner verifies empty outcomes with no winner.
func TestRoundOutcomeNoWinner(t *testing.T) {
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	r := round{
		ID: 0,
		Bets: []Bet{
			{Amount: 50, By: alice, On: bob},
		},
	}

	outcomes := r.roundOutcome()
	if len(outcomes) != 0 {
		t.Errorf("roundOutcome() with no winner = %d outcomes, want 0", len(outcomes))
	}
}

// TestBetsPercentage verifies percentage calculation of bets relative to money.
func TestBetsPercentage(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	GameState.AddPlayer(alice)
	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)

	GameState.AddRound()
	GameState.Rounds[0].AddClaim(alice) // alice has 100

	// Bet 20 on bob (20% of 100)
	GameState.Rounds[0].Bets = []Bet{
		{Amount: 20, By: alice, On: bob},
	}

	pct := GameState.betsPercentage(alice, GameState.Rounds[0])
	if pct != 20 {
		t.Errorf("betsPercentage() = %d, want 20", pct)
	}
}

// TestBetsPercentageNoMoney verifies zero percentage when player has no money.
func TestBetsPercentageNoMoney(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")

	GameState.AddRound()

	pct := GameState.betsPercentage(alice, GameState.Rounds[0])
	if pct != 0 {
		t.Errorf("betsPercentage() with no money = %d, want 0", pct)
	}
}

// TestPlayerTax verifies tax = playerMoney * 3 * (10 - betPct) / 100.
func TestPlayerTax(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	GameState.AddPlayer(alice)
	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)

	GameState.AddRound()
	GameState.Rounds[0].AddClaim(alice) // alice has 100
	// Alice bets 0 → betPct = 0, tax = 100 * 3 * 10 / 100 = 30
	tax := GameState.playerTax(alice, GameState.Rounds[0])
	if tax != 30 {
		t.Errorf("playerTax() = %d, want 30", tax)
	}
}

// TestPlayerTaxAtAndAboveThreshold verifies tax behaviour when bet% is exactly 10 (zero tax)
// and when bet% is strictly above 10 (negative tax, i.e. a bonus).
func TestPlayerTaxAtAndAboveThreshold(t *testing.T) {
	t.Run("exactly 10 percent bet tax is zero", func(t *testing.T) {
		setupGame()
		alice := makePlayer("1", "Alice")
		bob := makePlayer("2", "Bob")

		GameState.AddPlayer(alice)
		GameState.AddWheelOption(alice)
		GameState.AddWheelOption(bob)

		GameState.AddRound()
		GameState.Rounds[0].AddClaim(alice) // alice has 100
		// Alice bets 10 on bob → betPct = 10, (10-10)=0, tax = 0
		GameState.Rounds[0].Bets = []Bet{{Amount: 10, By: alice, On: bob}}
		tax := GameState.playerTax(alice, GameState.Rounds[0])
		if tax != 0 {
			t.Errorf("playerTax() = %d, want 0 when bet%% == 10", tax)
		}
	})

	t.Run("above 10 percent bet tax is negative (bonus)", func(t *testing.T) {
		setupGame()
		alice := makePlayer("1", "Alice")
		bob := makePlayer("2", "Bob")

		GameState.AddPlayer(alice)
		GameState.AddWheelOption(alice)
		GameState.AddWheelOption(bob)

		GameState.AddRound()
		GameState.Rounds[0].AddClaim(alice) // alice has 100
		// Alice bets 20 on bob → betPct = 20, (10-20)=-10, tax = (100*3*-10)/100 = -30
		GameState.Rounds[0].Bets = []Bet{{Amount: 20, By: alice, On: bob}}
		tax := GameState.playerTax(alice, GameState.Rounds[0])
		if tax != -30 {
			t.Errorf("playerTax() = %d, want -30 when bet%% == 20", tax)
		}
	})
}

// TestHasBetNotFound verifies false is returned when no matching bet exists.
func TestHasBetNotFound(t *testing.T) {
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")
	charlie := makePlayer("3", "Charlie")

	r := &round{ID: 0}
	r.Bets = []Bet{{Amount: 50, By: alice, On: charlie}}

	_, ok := r.HasBet(Bet{By: alice, On: bob}) // different target
	if ok {
		t.Error("HasBet() = true, want false for non-matching target")
	}

	_, ok2 := r.HasBet(Bet{By: bob, On: charlie}) // different bettor
	if ok2 {
		t.Error("HasBet() = true, want false for non-matching bettor")
	}
}

// TestWheelOptionsFallback verifies fallback to CurrentWheelOptions when round.ID >= len(Rounds).
func TestWheelOptionsFallback(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)
	GameState.AddRound() // only 1 round (index 0)

	// Pass a round with ID == len(Rounds) to trigger fallback
	futureRound := round{ID: 1}
	opts := GameState.wheelOptions(futureRound)
	current := GameState.CurrentWheelOptions()

	if len(opts) != len(current) {
		t.Errorf("wheelOptions fallback len = %d, want %d (CurrentWheelOptions)", len(opts), len(current))
	}
}
