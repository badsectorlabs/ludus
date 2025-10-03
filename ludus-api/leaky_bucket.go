package ludusapi

import (
	"encoding/json"
	"log"
	"math"
	"os"
	"sync"
	"time"
)

// A simple leaky bucket rate limiter for limiting the number of requests to a service
// Used to prevent a constantly restarting service from flooding the license server

// LeakyBucketState represents the state of our rate limiter that will be saved to disk.
type LeakyBucketState struct {
	Capacity   float64   `json:"capacity"`
	LastUpdate time.Time `json:"last_update"`
}

// LeakyBucket holds the configuration and state of our rate limiter.
type LeakyBucket struct {
	mutex     sync.Mutex
	stateFile string
	max       float64
	leakRate  float64 // Leaks per second
	state     LeakyBucketState
}

// NewLeakyBucket creates and initializes a new LeakyBucket.
// stateFile is the file to store the state of the leaky bucket
// max is the maximum number of requests allowed before limiting is applied
// leakRate is the rate at which the bucket leaks in requests per second
func NewLeakyBucket(stateFile string, max float64, leakRate float64) *LeakyBucket {
	bucket := &LeakyBucket{
		stateFile: stateFile,
		max:       max,
		leakRate:  leakRate,
	}

	bucket.loadState()
	return bucket
}

// loadState reads the state from the JSON file. If the file doesn't exist, it initializes a new state.
func (b *LeakyBucket) loadState() {
	data, err := os.ReadFile(b.stateFile)
	if err != nil {
		// If the file doesn't exist, initialize a new state.
		b.state = LeakyBucketState{
			Capacity:   0,
			LastUpdate: time.Now(),
		}
		return
	}

	json.Unmarshal(data, &b.state)
}

// saveState writes the current state to the JSON file.
func (b *LeakyBucket) saveState() {
	data, err := json.MarshalIndent(b.state, "", "  ")
	if err != nil {
		log.Printf("Error marshalling state: %v\n", err)
		return
	}
	os.WriteFile(b.stateFile, data, 0644)
}

// leak calculates how much the bucket has leaked since the last update and adjusts the capacity.
func (b *LeakyBucket) leak() {
	now := time.Now()
	elapsed := now.Sub(b.state.LastUpdate).Seconds()
	leakedAmount := elapsed * b.leakRate

	b.state.Capacity -= leakedAmount
	// Round the capacity to the nearest integer since we can't have a fractional number of requests and the float can be slightly less than the
	// actual capacity due to the magic of floating point math
	b.state.Capacity = math.Round(b.state.Capacity)
	if b.state.Capacity < 0 {
		b.state.Capacity = 0
	}

	b.state.LastUpdate = now
}

// Allow checks if a request is allowed. If it is, it updates the state and saves it.
func (b *LeakyBucket) Allow() bool {
	b.mutex.Lock()
	defer b.mutex.Unlock()

	// First, let the bucket leak.
	b.leak()

	// Check if there's enough capacity to add one more request.
	if b.state.Capacity < b.max {
		b.state.Capacity++
		b.saveState()
		return true
	}

	// The bucket is full, so the request is denied.
	return false
}
