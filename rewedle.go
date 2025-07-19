package main

import (
	"context"
	"encoding/json"
	"fmt"
	l "log"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	rewerse "github.com/ByteSizedMarius/rewerse-engineering/pkg"
	"github.com/alexedwards/scs/v2"
)

var (
	market   rewerse.Market
	product  rewerse.Product
	sessions *scs.SessionManager
	counter  int
	log      = l.Default()
)

const (
	sessionKey      = "rwdl-state"
	counterFileName = "current-rewedle"
)

type GuessResult int

const (
	Lower   GuessResult = 0
	Correct GuessResult = 1
	Higher  GuessResult = 2
)

type GuessResultRange struct {
	Color string
	Start float32
	End   float32
}

var (
	Green GuessResultRange = GuessResultRange{
		Color: "bg-green-600",
		Start: 0.0,
		End:   0.05,
	}

	Yellow = GuessResultRange{
		Color: "bg-yellow-500",
		Start: 0.06,
		End:   0.1,
	}

	Orange = GuessResultRange{
		Color: "bg-orange-600",
		Start: 0.11,
		End:   0.25,
	}

	Red = GuessResultRange{
		Color: "bg-red-500",
		Start: 0.25,
		End:   1.0,
	}

	ResultRanges = [4]GuessResultRange{Green, Yellow, Orange, Red}
)

func GetGuessRange(guess, actual float64) GuessResultRange {
	diff := math.Abs(float64(guess - actual))
	relativeError := diff / float64(actual)

	for _, r := range ResultRanges {
		if relativeError >= float64(r.Start) && relativeError <= float64(r.End) {
			return r
		}
	}

	return Red
}

type REWEdleState struct {
	Product           rewerse.Product `json:"product"`
	Guesses           []string
	GuessResults      []*GuessResult
	GuessResultRanges []*GuessResultRange
	Finished          bool
	Guessed           bool
}

func MakeState() REWEdleState {
	return REWEdleState{
		Product:           product,
		Guesses:           make([]string, 4),
		GuessResults:      make([]*GuessResult, 4),
		GuessResultRanges: make([]*GuessResultRange, 4),
		Finished:          false,
		Guessed:           false,
	}
}

func GetState(ctx context.Context) REWEdleState {
	val := sessions.Get(ctx, sessionKey)
	if val == nil {
		return MakeState()
	}

	switch v := val.(type) {
	case string:
		var state REWEdleState
		if err := json.Unmarshal([]byte(v), &state); err == nil {
			return state
		}
	}

	return MakeState()
}

func SaveState(ctx context.Context, state REWEdleState) {
	b, err := json.Marshal(state)
	if err != nil {
		fmt.Println("failed to marshal state:", err)
		return
	}
	sessions.Put(ctx, sessionKey, string(b))
}

func handleGuess(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	guessStr := strings.ReplaceAll(r.FormValue("guess"), ",", ".")
	parsedGuess, err := strconv.ParseFloat(guessStr, 64)
	if err != nil {
		http.Error(w, "bad guess", http.StatusBadRequest)
		return
	}

	state := GetState(r.Context())

	price := float64(state.Product.Listing.CurrentRetailPrice) / 100.0

	for idx := range state.Guesses {
		if state.Guesses[idx] == "" {
			state.Guesses[idx] = fmt.Sprintf("%.2f€", parsedGuess)

			guessRange := GetGuessRange(parsedGuess, price)
			state.GuessResultRanges[idx] = &guessRange

			var result GuessResult

			if parsedGuess < price {
				result = Higher
			} else {
				result = Lower
			}

			if guessRange == Green {
				result = Correct
				state.Finished = true
				state.Guessed = true
			}

			state.GuessResults[idx] = &result

			if idx == 3 {
				state.Finished = true
			}

			break
		}
	}

	SaveState(r.Context(), state)
	content(state).Render(r.Context(), w)
}

func jsonMarshal(state REWEdleState) string {
	type shareItem struct {
		Color   string `json:"color"`
		Result  string `json:"result"`
		Counter int    `json:"counter"`
	}

	var result []shareItem
	for idx := range state.Guesses {
		if state.GuessResults[idx] != nil && state.GuessResultRanges[idx] != nil {
			res := ""
			switch *state.GuessResults[idx] {
			case Lower:
				res = "Lower"
			case Higher:
				res = "Higher"
			case Correct:
				res = "Correct"
			}
			result = append(result, shareItem{
				Color:   state.GuessResultRanges[idx].Color,
				Result:  res,
				Counter: counter,
			})
		}
	}
	b, _ := json.Marshal(result)
	return string(b)
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	state := MakeState()
	SaveState(r.Context(), state)

	index(state).Render(r.Context(), w)
}

func incrementCounter() error {
	if _, err := os.Stat(counterFileName); os.IsNotExist(err) {
		err = os.WriteFile(counterFileName, []byte("1"), 0644)
		counter = 1
		if err != nil {
			return fmt.Errorf("failed to create file: %v", err)
		}
		return nil
	}

	data, err := os.ReadFile(counterFileName)
	if err != nil {
		counter = 1
		return fmt.Errorf("failed to read file: %v", err)
	}

	trimmed := strings.TrimSpace(string(data))
	current, err := strconv.Atoi(trimmed)
	if err != nil {
		counter = 1
		return fmt.Errorf("invalid number in file: %v", err)
	}

	current++
	err = os.WriteFile(counterFileName, []byte(strconv.Itoa(current)), 0644)
	if err != nil {
		counter = 1
		return fmt.Errorf("failed to write file: %v", err)
	}

	counter = current

	return nil
}

func main() {
	err := rewerse.SetCertificate("keys/cert.pem", "keys/private.key")
	if err != nil {
		log.Fatal("invalid certificates: ", err)
	}

	// The author of rewerse was a bit silly and hardcoded their zip code
	// which requires us to use markets in Ludwigshafen lmao
	markets, err := rewerse.MarketSearch("Ludwigshafen")
	if err != nil {
		log.Fatal("could not find markets in ludwigshafen: ", err)
	}

	market = markets[0]

	log.Print("found market: ", market)

	opts := rewerse.ProductOpts{
		Page:           0,
		ObjectsPerPage: 250,
	}

	results, err := rewerse.GetProducts(market.ID, "", &opts)
	products := results.Data.Products.Products

	if err != nil {
		log.Fatal("failed fetching products: ", err)
	}

	log.Print("found ", len(products), " products")

	idx := rand.IntN(len(products))
	product = products[idx]

	log.Print("found product: ", product.Title, " listed for ", float64(product.Listing.CurrentRetailPrice)/100.0, "€")

	sessions = scs.New()
	sessions.Lifetime = 24 * time.Hour
	sessions.Cookie.Name = sessionKey

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/guess", handleGuess)

	if err := incrementCounter(); err != nil {
		log.Fatal("failed incrementing counter: ", err)
	}

	if err := http.ListenAndServe("0.0.0.0:8080", sessions.LoadAndSave(mux)); err != nil {
		log.Fatal(err)
	}
}
