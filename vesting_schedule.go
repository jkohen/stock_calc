package main

import (
	"encoding/csv"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
)

// Grant represents a single stock option grant.
type Grant struct {
	Name          string
	Shares        int
	StrikePrice   float64
	CliffMonths   int
	VestingMonths int
	GrantDate     time.Time
}

// VestingEvent represents a single vesting event.
type VestingEvent struct {
	Date         time.Time
	VestedShares int
}

// DateFormat defines the expected date format for input and output.
const DateFormat = "2006-01-02"

func main() {
	filePathPtr := flag.String("file", "", "Path to the grants CSV file (required)")
	exerciseValuePtr := flag.Float64("exercise", 0.0, "Current exercise value per share (required)")
	endDateStrPtr := flag.String("end-date", "", "Calculate vesting up to this date (YYYY-MM-DD) (required)")
	printVestingSchedulePtr := flag.Bool("print-schedule", false, "Print the full vesting schedule for each grant")

	flag.Parse()

	// --- Validation ---
	var validationErrors []string
	if *filePathPtr == "" {
		validationErrors = append(validationErrors, "-file flag is required.")
	}
	if *exerciseValuePtr == 0.0 {
		// Allow 0, but maybe warn? For now, treat as required if non-zero value expected.
		// Let's keep the original logic: require a non-zero value.
		validationErrors = append(validationErrors, "-exercise flag with a non-zero value is required.")
	}
	if *endDateStrPtr == "" {
		validationErrors = append(validationErrors, "-end-date flag is required.")
	}

	var endDate time.Time
	var err error
	if *endDateStrPtr != "" {
		endDate, err = time.Parse(DateFormat, *endDateStrPtr)
		if err != nil {
			validationErrors = append(validationErrors, fmt.Sprintf("Invalid format for -end-date: %v. Use %s.", err, DateFormat))
		}
	}

	if len(validationErrors) > 0 {
		fmt.Println("Errors:")
		for _, msg := range validationErrors {
			fmt.Printf("  - %s\n", msg)
		}
		fmt.Println("\nUsage: go run vesting_schedule.go -file <path_to_csv> -exercise <value> -end-date <YYYY-MM-DD>")
		flag.PrintDefaults()
		return
	}
	// --- End Validation ---

	grants, err := loadGrants(*filePathPtr)
	if err != nil {
		fmt.Println("Error loading grants:", err)
		return
	}

	fmt.Printf("\nVesting Status as of %s (Exercise Value: $%.2f):\n", endDate.Format(DateFormat), *exerciseValuePtr)
	// Print header once
	fmt.Printf("\n%-20s %-12s %-14s %-20s\n", "Grant Name", "Vesting Date", "Total Vested", "Accumulated Value")
	fmt.Println(strings.Repeat("-", 70)) // Adjust separator length

	totalVestedSharesByEndDate := 0
	totalAccumulatedValue := 0.0
	for _, grant := range grants {
		schedule := calculateVestingSchedule(grant)
		vestedSharesByEndDate, accumulatedValue := printLatestVestingEventBefore(grant.Name, schedule, grant.StrikePrice, *exerciseValuePtr, endDate)
		totalVestedSharesByEndDate += vestedSharesByEndDate
		totalAccumulatedValue += accumulatedValue
	}

	fmt.Println(strings.Repeat("-", 70)) // Adjust separator length
	fmt.Printf("%-20s %-12s %-14d $%-19.2f\n",
		"Total",
		endDate.Format(DateFormat),
		totalVestedSharesByEndDate, // Print total vested by this date
		totalAccumulatedValue)

	if *printVestingSchedulePtr {
		fmt.Println()
		for _, grant := range grants {
			schedule := calculateVestingSchedule(grant)
			printVestingSchedule(schedule, grant.StrikePrice, *exerciseValuePtr) // Print each schedule with zero strike price
			fmt.Println()
		}
	}
}

func loadGrants(filePath string) ([]Grant, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("opening file: %w", err)
	}
	defer file.Close()

	reader := csv.NewReader(file)
	var grants []Grant
	headerSkipped := false
	lineNumber := 0

	for {
		lineNumber++
		record, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading csv line %d: %w", lineNumber, err)
		}

		if !headerSkipped {
			headerSkipped = true
			continue // Skip the header row
		}

		if len(record) != 6 {
			return nil, fmt.Errorf("invalid number of columns in CSV row %d (expected 6): %v", lineNumber, record)
		}

		// Trim spaces from all fields
		for i := range record {
			record[i] = strings.TrimSpace(record[i])
		}

		name := record[0]
		shares, err := strconv.Atoi(record[1])
		if err != nil {
			return nil, fmt.Errorf("invalid number of shares on line %d ('%s'): %w", lineNumber, record[1], err)
		}
		strikePrice, err := strconv.ParseFloat(record[2], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid strike price on line %d ('%s'): %w", lineNumber, record[2], err)
		}

		cliffDurationStr := record[3]
		cliffDuration, err := strconv.Atoi(cliffDurationStr)
		if err != nil {
			return nil, fmt.Errorf("invalid cliff duration (months) on line %d ('%s'): %w", lineNumber, cliffDurationStr, err)
		}

		vestingDurationStr := record[4]
		vestingDuration, err := strconv.Atoi(vestingDurationStr)
		if err != nil {
			return nil, fmt.Errorf("invalid vesting duration (months) on line %d ('%s'): %w", lineNumber, vestingDurationStr, err)
		}
		if vestingDuration <= 0 {
			return nil, fmt.Errorf("vesting duration must be positive on line %d ('%s')", lineNumber, vestingDurationStr)
		}

		grantDate, err := time.Parse(DateFormat, record[5])
		if err != nil {
			return nil, fmt.Errorf("invalid grant date format on line %d ('%s', expected %s): %w", lineNumber, record[5], DateFormat, err)
		}

		grant := Grant{
			Name:          name,
			Shares:        shares,
			StrikePrice:   strikePrice,
			CliffMonths:   cliffDuration,
			VestingMonths: vestingDuration,
			GrantDate:     grantDate,
		}
		grants = append(grants, grant)
	}

	return grants, nil
}

func calculateVestingSchedule(grant Grant) []VestingEvent {
	var schedule []VestingEvent
	vestingInterval := time.Hour * 24 * 30 // Assume monthly vesting for simplicity
	totalVestingMonths := grant.VestingMonths
	sharesPerInterval := grant.Shares / totalVestingMonths

	accumulatedShares := grant.CliffMonths * sharesPerInterval
	if grant.CliffMonths > 0 {
		schedule = append(schedule, VestingEvent{
			Date:         grant.GrantDate.AddDate(0, grant.CliffMonths, 0),
			VestedShares: accumulatedShares,
		})
	}
	for i := grant.CliffMonths + 1; i <= totalVestingMonths; i++ {
		// BUG the vesting date is typically the same day every month, not 30 days later.
		vestingDate := grant.GrantDate.Add(time.Duration(i) * vestingInterval)
		vestedShares := sharesPerInterval
		if i == totalVestingMonths {
			vestedShares = grant.Shares - accumulatedShares // Ensure all shares are vested by the end
		}
		accumulatedShares += vestedShares
		schedule = append(schedule, VestingEvent{
			Date:         vestingDate,
			VestedShares: vestedShares,
		})
	}

	return schedule
}

// printLatestVestingEventBefore finds the latest vesting event on or before the endDate
// and prints its details along with the total accumulated value up to that point.
func printLatestVestingEventBefore(grantName string, schedule []VestingEvent, strikePrice, exerciseValue float64, endDate time.Time) (int, float64) {
	var latestEvent *VestingEvent = nil
	totalVestedSharesByEndDate := 0

	currentAccumulatedShares := 0
	for _, event := range schedule {
		// Check if the event date is on or before the end date
		// Use !After for inclusive comparison (<=)
		if !event.Date.After(endDate) {
			currentAccumulatedShares += event.VestedShares
			// Keep track of the pointer to the latest qualifying event
			// Need to capture the loop variable correctly if using its address later,
			// so create a temporary copy.
			tempEvent := event // Create a copy
			latestEvent = &tempEvent
			totalVestedSharesByEndDate = currentAccumulatedShares
		} else {
			// Since schedule is chronological, we can stop early
			break
		}
	}

	if latestEvent != nil {
		accumulatedValue := float64(totalVestedSharesByEndDate) * (exerciseValue - strikePrice)
		if accumulatedValue < 0 {
			accumulatedValue = 0 // Value cannot be negative
		}
		fmt.Printf("%-20s %-12s %-14d $%-19.2f\n",
			grantName,
			latestEvent.Date.Format(DateFormat),
			totalVestedSharesByEndDate, // Print total vested by this date
			accumulatedValue)
		return totalVestedSharesByEndDate, accumulatedValue
	} else {
		// No vesting events occurred on or before the end date for this grant
		fmt.Printf("%-20s %-12s %-14d $%-19.2f\n",
			grantName,
			"N/A", // No vesting date
			0,     // 0 shares vested
			0.0)   // 0 value
		return 0, 0.0
	}
}

// --- Old printVestingSchedule function (for reference, now replaced) ---
func printVestingSchedule(schedule []VestingEvent, strikePrice, exerciseValue float64) {
	accumulatedValue := 0.0
	totalVestedShares := 0

	fmt.Printf("%-12s %-14s %-20s\n", "Vesting Date", "Vested Shares", "Accumulated Value")
	fmt.Println(strings.Repeat("-", 46))

	for _, event := range schedule {
		totalVestedShares += event.VestedShares
		currentValue := float64(totalVestedShares) * (exerciseValue - strikePrice)
		if currentValue < 0 {
			currentValue = 0 // Exercise value cannot be negative
		}
		accumulatedValue = currentValue
		fmt.Printf("%-12s %-14d $%.2f\n", event.Date.Format("2006-01-02"), event.VestedShares, accumulatedValue)
	}
}
