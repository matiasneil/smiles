package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"smiles/model"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tele "gopkg.in/telebot.v3"
)

// input parameters
var (
	departureDateStr       = "2025-05-10" // primer día para la ida
	returnDateStr          = "2025-05-20" // primer día para la vuelta
	originAirportCode      = "EZE"        // aeropuerto de origen
	destinationAirportCode = "PUJ"        // aeropuerto de destino
	daysToQuery            = 1            // días corridos para buscar ida y vuelta
)

const (
	// only used for dev
	readFromFile            = false
	useCommandLineArguments = true
	mockResponseFilePath    = "data/response.json"

	dateLayout        = "2006-01-02"
	bigMaxMilesNumber = 9_999_999
	flightSearchApi   = "api-air-flightsearch-green.smiles.com.br"
	boardingTaxApi    = "api-airlines-boarding-tax-green.smiles.com.br"
)

func main() {
	client := http.Client{}

	pref := tele.Settings{
		Token:  os.Getenv("TOKEN"),
		Poller: &tele.LongPoller{Timeout: 10 * time.Second},
	}

	b, err := tele.NewBot(pref)
	if err != nil {
		log.Fatal(err)
		return
	}

	b.Handle("/search", func(c tele.Context) error {
		args := c.Args()
		paramsErr := validateParameters(args)

		if len(paramsErr) != 0 {
			return c.Send(err)
		}

		startingDepartureDate, err := time.Parse(dateLayout, departureDateStr)
		startingReturningDate, err := time.Parse(dateLayout, returnDateStr)

		_ = startingDepartureDate
		_ = startingReturningDate

		if err != nil {
			return c.Send("Error parsing dates")
		}

		c.Send("<i>Buscando... <b>"+originAirportCode+" - "+destinationAirportCode+"</b></i>", &tele.SendOptions{
			ParseMode: "HTML",
		})

		departuresCh := make(chan model.Result, daysToQuery)
		returnsCh := make(chan model.Result, daysToQuery)

		var wg sync.WaitGroup

		for i := 0; i < daysToQuery; i++ {
			departureDate := startingDepartureDate.AddDate(0, 0, i)
			returnDate := startingReturningDate.AddDate(0, 0, i)

			wg.Add(2)
			go makeRequest(&wg, departuresCh, &client, departureDate, originAirportCode, destinationAirportCode)
			// inverting airports and changing date to query returns
			go makeRequest(&wg, returnsCh, &client, returnDate, destinationAirportCode, originAirportCode)
		}

		wg.Wait()
		close(departuresCh)
		close(returnsCh)

		var departureResults []model.Result
		var returnResults []model.Result

		for elem := range departuresCh {
			departureResults = append(departureResults, elem)
		}

		for elem := range returnsCh {
			returnResults = append(returnResults, elem)
		}

		sortResults(departureResults)
		sortResults(returnResults)

		c.Send("<b>VUELOS DE IDA</b>", &tele.SendOptions{
			ParseMode: "HTML",
		})

		departureResultsHtml := processResults(&client, departureResults)
		c.Send(departureResultsHtml, &tele.SendOptions{
			ParseMode: "HTML",
		})

		c.Send("<b>VUELOS DE VUELTA</b>", &tele.SendOptions{
			ParseMode: "HTML",
		})

		returnResultsHtml := processResults(&client, returnResults)
		c.Send(returnResultsHtml, &tele.SendOptions{
			ParseMode: "HTML",
		})

		return nil
	})

	b.Start()
}

func sortResults(r []model.Result) {
	sort.Slice(r, func(i, j int) bool {
		return r[i].QueryDate.Before(r[j].QueryDate)
	})
}

func makeRequest(wg *sync.WaitGroup, ch chan<- model.Result, c *http.Client, startingDate time.Time, originAirport string, destinationAirport string) {
	defer wg.Done()

	var body []byte
	var err error
	data := model.Data{}

	u := createURL(startingDate.Format(dateLayout), originAirport, destinationAirport) // Encode and assign back to the original query.
	req := createRequest(u, flightSearchApi)

	//fmt.Println("Making request with URL: ", req.URL.String())
	//fmt.Printf("Consultando %s - %s para el día %s \n", originAirport, destinationAirport, startingDate.Format(dateLayout))

	// only for dev purposes
	if readFromFile {
		fmt.Println("Reading from file ", mockResponseFilePath)
		body, err = os.ReadFile(mockResponseFilePath)
		if err != nil {
			log.Fatal("error reading file")
		}
	} else {
		res, err := c.Do(req)
		if err != nil {
			log.Fatal("Error making request ", err)
		}

		body, err = ioutil.ReadAll(res.Body)
		if body == nil {
			log.Fatal("Empty result")
		}
	}

	if err := json.Unmarshal(body, &data); err != nil {
		log.Fatal("Error unmarshalling data ", err)
	}

	ch <- model.Result{Data: data, QueryDate: startingDate}
}

func createRequest(u url.URL, authority string) *http.Request {
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		log.Fatal("Error creating request ", err)
	}

	// headers
	req.Header.Add("x-api-key", "aJqPU7xNHl9qN3NVZnPaJ208aPo2Bh2p2ZV844tw")
	req.Header.Add("region", "ARGENTINA")
	req.Header.Add("origin", "https://www.smiles.com.ar")
	req.Header.Add("referer", "https://www.smiles.com.ar")
	req.Header.Add("channel", "web")
	req.Header.Add("authority", authority)
	req.Header.Add("user-agent", "Mozilla/5.0")
	return req
}

func createURL(departureDate string, originAirport string, destinationAirport string) url.URL {
	u := url.URL{
		Scheme:   "https",
		Host:     flightSearchApi,
		RawQuery: "adults=1&cabinType=all&children=0&currencyCode=ARS&infants=0&isFlexibleDateChecked=false&tripType=2&forceCongener=true&r=ar",
		Path:     "/v1/airlines/search",
	}
	q := u.Query()
	q.Add("departureDate", departureDate)
	q.Add("originAirportCode", originAirport)
	q.Add("destinationAirportCode", destinationAirport)
	u.RawQuery = q.Encode()
	return u
}

func createTaxURL(departureFlight *model.Flight, departureFare *model.Fare) url.URL {
	u := url.URL{
		Scheme:   "https",
		Host:     boardingTaxApi,
		RawQuery: "adults=1&children=0&infants=0&highlightText=SMILES_CLUB",
		Path:     "/v1/airlines/flight/boardingtax",
	}
	q := u.Query()
	q.Add("type", "SEGMENT_1")
	q.Add("uid", departureFlight.UId)
	q.Add("fareuid", departureFare.UId)
	u.RawQuery = q.Encode()
	return u
}

func getSmilesClubFare(f *model.Flight) *model.Fare {
	for i, v := range f.FareList {
		if v.FType == "SMILES_CLUB" {
			return &f.FareList[i]
		}
	}
	fmt.Println("WARN: SMILES_CLUB fare not fund")
	// for the sake of simplicity returning ridiculous default big number when fare not found
	return &model.Fare{Miles: bigMaxMilesNumber}
}

func validateParameters(a []string) string {
	originAirportCode = a[0]
	if len(originAirportCode) != 3 {
		return "Error: El aeropuerto de origen " + originAirportCode + " no es válido"
	}

	destinationAirportCode = a[1]
	if len(destinationAirportCode) != 3 {
		return "Error: El aeropuerto de destino " + destinationAirportCode + " no es válido"
	}

	departureDateStr = a[2]
	_, err := time.Parse(dateLayout, departureDateStr)
	if err != nil {
		return "Error: La fecha de salida " + departureDateStr + " no es válida"
	}

	returnDateStr = a[3]
	_, err = time.Parse(dateLayout, returnDateStr)
	if err != nil {
		return "Error: La fecha de regreso " + returnDateStr + " no es válida"
	}

	v, err := strconv.ParseInt(a[4], 10, 64)
	if err != nil {
		return "Error: La cantidad de días " + string(v) + " no es válida"
	}

	if v > 10 {
		return "Error: La cantidad de días no puede ser mayor a 10;"
	}
	daysToQuery = int(v)

	return ""
}

func processResults(c *http.Client, r []model.Result) string {
	var sb strings.Builder

	// using the first flight as cheapest default
	var cheapestFlight *model.Flight
	cheapestFare := &model.Fare{
		Miles: bigMaxMilesNumber,
	}

	// loop through all results
	for _, v := range r {
		var cheapestFlightDay *model.Flight
		cheapestFareDay := &model.Fare{
			Miles: bigMaxMilesNumber,
		}

		// loop through all flights by day
		for _, f := range v.Data.RequestedFlightSegmentList[0].FlightList {
			smilesClubFare := getSmilesClubFare(&f)
			if cheapestFareDay.Miles > smilesClubFare.Miles {
				cheapestFlightDay = &f
				cheapestFareDay = smilesClubFare
			}
		}

		if cheapestFare.Miles > cheapestFareDay.Miles {
			cheapestFlight = cheapestFlightDay
			cheapestFare = cheapestFareDay
		}

		if cheapestFareDay.Miles != bigMaxMilesNumber {
			sb.WriteString("<b>●</b> " + cheapestFlightDay.Departure.Date.Format(dateLayout) + ": " + cheapestFlightDay.Departure.Airport.Code + " - " + cheapestFlightDay.Arrival.Airport.Code + ", " + cheapestFlightDay.Cabin + ", " + cheapestFlightDay.Airline.Name + ", " + strconv.Itoa(cheapestFlightDay.Stops) + " escalas, " + strconv.Itoa(cheapestFareDay.Miles) + " millas \n")
		}
	}

	if cheapestFare.Miles != bigMaxMilesNumber {
		boardingTax := getTaxForFlight(c, cheapestFlight, cheapestFare)

		sb.WriteString("<b>●</b> " + cheapestFlight.Departure.Date.Format(dateLayout) + ": " + cheapestFlight.Departure.Airport.Code + " - " + cheapestFlight.Arrival.Airport.Code + ", " + cheapestFlight.Cabin + ", " + cheapestFlight.Airline.Name + ", " + strconv.Itoa(cheapestFlight.Stops) + " escalas, " + strconv.Itoa(cheapestFare.Miles) + " millas, " + strconv.FormatFloat(float64(boardingTax.Totals.Total.Money), 'f', 6, 64) + " de Tasas e impuestos \n")
	}

	return sb.String()
}

func getTaxForFlight(c *http.Client, flight *model.Flight, fare *model.Fare) *model.BoardingTax {
	u := createTaxURL(flight, fare)
	r := createRequest(u, boardingTaxApi)
	var body []byte
	var data model.BoardingTax

	res, err := c.Do(r)
	if err != nil {
		log.Fatal("Error making request ", err)
	}

	body, err = ioutil.ReadAll(res.Body)
	if body == nil {
		log.Fatal("Empty result")
	}

	if err := json.Unmarshal(body, &data); err != nil {
		log.Fatal("Error unmarshalling data ", err)
	}

	return &data
}
