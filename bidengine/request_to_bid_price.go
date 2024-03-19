package bidengine

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	sdk "github.com/cosmos/cosmos-sdk/types"

	dtypes "github.com/akash-network/akash-api/go/node/deployment/v1beta3"
	"github.com/akash-network/akash-api/go/node/types/v1beta3"
)

// Define default price targets as constants
const (
	defaultCPUTarget         = 1.60
	defaultMemoryTarget      = 0.80
	defaultHDEphemeralTarget = 0.02
	defaultHDPersHDDTarget   = 0.01
	defaultHDPersSSDTarget   = 0.03
	defaultHDPersNVMETarget  = 0.04
	defaultEndpointTarget    = 0.05
	defaultIPTarget          = 5.00

	averageBlockTimeSeconds = 6.117 // Adjust as per the actual average block time
	daysPerMonth            = 30.437
	blocksPerMonth          = (60 / averageBlockTimeSeconds) * 24 * 60 * daysPerMonth
)

type ResourceRequests struct {
	CPURequested              float64
	MemoryRequested           int64
	EphemeralStorageRequested int64
	HDDPersStorageRequested   int64
	SSDPersStorageRequested   int64
	NVMePersStorageRequested  int64
	IPsRequested              int64
	EndpointsRequested        int64
}

type PriceTargets struct {
	CPUTarget         float64
	MemoryTarget      float64
	HDEphemeralTarget float64
	HDPersHDDTarget   float64
	HDPersSSDTarget   float64
	HDPersNVMETarget  float64
	EndpointTarget    float64
	IPTarget          float64
	GPUMappings       map[string]float64
}

// DeploymentOrder represents the structure of the data received from the Akash Provider.
type DeploymentOrder struct {
	Price          *Price          `json:"price"`
	PricePrecision int             `json:"price_precision"`
	Resources      json.RawMessage `json:"resources"`
}

// Price represents the price structure in the deployment order.
type Price struct {
	Denom  string `json:"denom"`
	Amount string `json:"amount"`
}

// SpecialPricing checks if the AKASH_OWNER is in a predefined list and applies special pricing if so.
func SpecialPricing(owner string) bool {
	specialAccounts := map[string]bool{
		"akash1fxa9ss3dg6nqyz8aluyaa6svypgprk5tw9fa4q": true,
		"akash1fhe3uk7d95vvr69pna7cxmwa8777as46uyxcz8": true,
	}
	return specialAccounts[owner]
}

// checkWhitelist checks if the AKASH_OWNER is in the whitelist defined by the WHITELIST_URL.
func checkWhitelist(owner string) error {
	whitelistURL := os.Getenv("WHITELIST_URL")
	whitelistURL = strings.Trim(whitelistURL, "\"") // Trim any double quotes from the URL

	if whitelistURL == "" {
		return nil // No whitelist URL set, skip checking
	}

	whitelistFile := "/tmp/price-script.whitelist"
	if shouldFetchWhitelist(whitelistFile) {
		if err := fetchWhitelist(whitelistURL, whitelistFile); err != nil {
			return fmt.Errorf("error fetching whitelist: %w", err)
		}
	}

	if err := verifyInWhitelist(whitelistFile, os.Getenv("AKASH_OWNER")); err != nil {
		return err
	}

	return nil
}

// shouldFetchWhitelist checks if the whitelist file should be fetched again.
func shouldFetchWhitelist(whitelistFile string) bool {
	fileInfo, err := os.Stat(whitelistFile)
	if os.IsNotExist(err) || time.Since(fileInfo.ModTime()) > 10*time.Minute {
		return true
	}
	return false
}

// fetchWhitelist downloads the whitelist from the given URL and saves it.
func fetchWhitelist(whitelistURL, whitelistFile string) error {
	resp, err := http.Get(whitelistURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP request error: %s", resp.Status)
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(whitelistFile, body, 0644)
}

// verifyInWhitelist checks if the given owner is in the whitelist file.
func verifyInWhitelist(whitelistFile, owner string) error {
	file, err := os.Open(whitelistFile)
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == owner {
			return nil // Owner is in the whitelist
		}
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return fmt.Errorf("%s is not whitelisted", owner)
}

// getAKTPrice fetches the current price of AKT from the APIs, caching it.
func getAKTPrice() (float64, error) {
	cacheFile := "/tmp/aktprice.cache"
	price, err := readCachedPrice(cacheFile)
	if err == nil {
		return price, nil
	}

	price, err = fetchPriceFromAPI()
	if err != nil {
		return 0, err
	}

	if err := cachePrice(cacheFile, price); err != nil {
		return 0, err
	}

	return price, nil
}

// readCachedPrice reads the AKT price from the cache file.
func readCachedPrice(cacheFile string) (float64, error) {
	fileInfo, err := os.Stat(cacheFile)
	if os.IsNotExist(err) || time.Since(fileInfo.ModTime()) > 60*time.Minute {
		return 0, fmt.Errorf("cache file does not exist or is expired")
	}

	data, err := ioutil.ReadFile(cacheFile)
	if err != nil {
		return 0, err
	}

	price, err := strconv.ParseFloat(string(data), 64)
	if err != nil {
		return 0, err
	}

	return price, nil
}

// fetchPriceFromAPI tries to fetch the AKT price from primary and fallback APIs.
func fetchPriceFromAPI() (float64, error) {
	primaryURL := "https://api-osmosis.imperator.co/tokens/v2/price/AKT"
	fallbackURL := "https://api.coingecko.com/api/v3/simple/price?ids=akash-network&vs_currencies=usd"

	price, err := fetchPriceFromURL(primaryURL)
	if err != nil {
		fmt.Println("Primary API failed, trying fallback")
		return fetchPriceFromURL(fallbackURL)
	}

	return price, nil
}

// fetchPriceFromURL fetches the AKT price from a given URL.
func fetchPriceFromURL(url string) (float64, error) {
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var data interface{}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return 0, err
	}

	return extractPrice(data), nil
}

// extractPrice extracts the AKT price from the API response.
func extractPrice(data interface{}) float64 {
	switch v := data.(type) {
	case map[string]interface{}:
		if price, ok := v["price"].(float64); ok {
			return price
		}
		if nested, ok := v["akash-network"].(map[string]interface{}); ok {
			if price, ok := nested["usd"].(float64); ok {
				return price
			}
		}
	}
	return 0
}

// cachePrice writes the AKT price to the cache file.
func cachePrice(cacheFile string, price float64) error {
	return ioutil.WriteFile(cacheFile, []byte(fmt.Sprintf("%f", price)), 0644)
}

// 	 computes the total requested resources from the GroupSpec
func calculateRequestedResources(gSpec *dtypes.GroupSpec) ResourceRequests {
	var result ResourceRequests

	for _, resourceUnit := range gSpec.Resources {

		if resourceUnit.Resources.CPU != nil {
			cpuUnits := resourceUnit.Resources.CPU.Units.Val.Int64() // Get the CPU units in milliCPUs
			cpuCores := float64(cpuUnits) / 1000.0                   // Convert milliCPUs to CPU cores
			result.CPURequested += cpuCores * float64(resourceUnit.Count)
		}

		if resourceUnit.Resources.Memory != nil {
			memoryBytes := resourceUnit.Resources.Memory.Quantity.Val.Int64()
			memoryGB := memoryBytes / (1024 * 1024 * 1024) // Convert bytes to gigabytes
			result.MemoryRequested += memoryGB * int64(resourceUnit.Count)
		}

		for _, storage := range resourceUnit.Resources.Storage {
			// Default to using the 'name' field as the class if 'class' attribute is not found.
			storageClass := storage.Name

			// Look for 'class' in attributes to override the default if present.
			for _, attr := range storage.Attributes {
				if attr.Key == "class" {
					storageClass = attr.Value
					break
				}
			}

			storageBytes := storage.Quantity.Val.Int64()
			storageGB := storageBytes / (1024 * 1024 * 1024) // Convert bytes to gigabytes

			switch storageClass {
			case "ephemeral", "default":
				result.EphemeralStorageRequested += storageGB * int64(resourceUnit.Count)
			case "beta1":
				result.HDDPersStorageRequested += storageGB * int64(resourceUnit.Count)
			case "beta2":
				result.SSDPersStorageRequested += storageGB * int64(resourceUnit.Count)
			case "beta3":
				result.NVMePersStorageRequested += storageGB * int64(resourceUnit.Count)
			}
		}

		for _, endpoint := range resourceUnit.Resources.Endpoints {
			result.EndpointsRequested += int64(resourceUnit.Count) // Assuming 1 endpoint per resource unit count
			if endpoint.Kind == v1beta3.Endpoint_LEASED_IP {
				result.IPsRequested += int64(resourceUnit.Count) // Assuming 1 IP per resource unit count
			}
		}
	}

	return result
}

// getEnvFloat gets an environment variable as a float, returning a default value if not set or invalid
func getEnvFloat(envVar string, defaultValue float64) float64 {
	if val, ok := os.LookupEnv(envVar); ok {
		if floatVal, err := strconv.ParseFloat(val, 64); err == nil {
			return floatVal
		}
	}
	return defaultValue
}

// setPriceTargets sets the price targets from environment variables or uses defaults
func setPriceTargets() PriceTargets {
	gpuMappingsStr := os.Getenv("PRICE_TARGET_GPU_MAPPINGS") // Assuming this environment variable contains the mappings
	gpuMappings, err := parseGPUPriceMappings(gpuMappingsStr)
	if err != nil {
		log.Fatalf("Error parsing GPU mappings: %v", err)
	}

	return PriceTargets{
		CPUTarget:         getEnvFloat("PRICE_TARGET_CPU", defaultCPUTarget),
		MemoryTarget:      getEnvFloat("PRICE_TARGET_MEMORY", defaultMemoryTarget),
		HDEphemeralTarget: getEnvFloat("PRICE_TARGET_HD_EPHEMERAL", defaultHDEphemeralTarget),
		HDPersHDDTarget:   getEnvFloat("PRICE_TARGET_HD_PERS_HDD", defaultHDPersHDDTarget),
		HDPersSSDTarget:   getEnvFloat("PRICE_TARGET_HD_PERS_SSD", defaultHDPersSSDTarget),
		HDPersNVMETarget:  getEnvFloat("PRICE_TARGET_HD_PERS_NVME", defaultHDPersNVMETarget),
		EndpointTarget:    getEnvFloat("PRICE_TARGET_ENDPOINT", defaultEndpointTarget),
		IPTarget:          getEnvFloat("PRICE_TARGET_IP", defaultIPTarget),
		GPUMappings:       gpuMappings,
	}
}

func calculateTotalCostUsdTarget(resourceRequests ResourceRequests, priceTargets PriceTargets) float64 {
	var totalCostUsdTarget float64

	cpuCost := float64(resourceRequests.CPURequested) * priceTargets.CPUTarget
	totalCostUsdTarget += cpuCost

	memoryCost := float64(resourceRequests.MemoryRequested) * priceTargets.MemoryTarget
	totalCostUsdTarget += memoryCost

	ephemeralStorageCost := float64(resourceRequests.EphemeralStorageRequested) * priceTargets.HDEphemeralTarget
	totalCostUsdTarget += ephemeralStorageCost

	hddPersStorageCost := float64(resourceRequests.HDDPersStorageRequested) * priceTargets.HDPersHDDTarget
	totalCostUsdTarget += hddPersStorageCost

	ssdPersStorageCost := float64(resourceRequests.SSDPersStorageRequested) * priceTargets.HDPersSSDTarget
	totalCostUsdTarget += ssdPersStorageCost

	nvmePersStorageCost := float64(resourceRequests.NVMePersStorageRequested) * priceTargets.HDPersNVMETarget
	totalCostUsdTarget += nvmePersStorageCost

	endpointCost := float64(resourceRequests.EndpointsRequested) * priceTargets.EndpointTarget
	totalCostUsdTarget += endpointCost

	ipCost := float64(resourceRequests.IPsRequested) * priceTargets.IPTarget
	totalCostUsdTarget += ipCost

	return totalCostUsdTarget
}

func calculateBlockRates(totalCostUsdTarget float64, usdPerAkt float64, precision int) (float64, float64, string) {
	totalCostAktTarget := totalCostUsdTarget / usdPerAkt
	totalCostUaktTarget := totalCostAktTarget * 1000000 // Convert AKT to microAKT (uakt)

	ratePerBlockUakt := totalCostUaktTarget / blocksPerMonth
	ratePerBlockUsd := totalCostUsdTarget / blocksPerMonth

	// Format to the desired precision with 16 decimal places and append "uakt"
	totalCostUaktStr := fmt.Sprintf("%.*f", 16, ratePerBlockUakt) + "uakt"

	return ratePerBlockUakt, ratePerBlockUsd, totalCostUaktStr
}

// handleDenomLogic processes the logic based on the received denom
func handleDenomLogic(denom string, ratePerBlockUakt float64, ratePerBlockUsd float64, precision int, amount sdk.Dec) (string, error) {
	switch denom {
	case "uakt":
		if ratePerBlockUakt > amount.MustFloat64() { // Convert sdk.Dec to float64 for comparison
			return "", fmt.Errorf("requested rate is too low. min expected %.*f%s", precision, ratePerBlockUakt, denom)
		}
		return fmt.Sprintf("%.*f", precision, ratePerBlockUakt), nil

	case "ibc/12C6A0C374171B595A0A9E18B83FA09D295FB1F2D8C6DAA3AC28683471752D84",
		"ibc/170C677610AC31DF0904FFE09CD3B5C657492170E7E52372E48756B71E56F2F1":
		ratePerBlockUsdNormalized := ratePerBlockUsd * 1000000
		if ratePerBlockUsdNormalized > amount.MustFloat64() {
			return "", fmt.Errorf("requested rate is too low. min expected %.*f%s", precision, ratePerBlockUsdNormalized, denom)
		}
		return fmt.Sprintf("%.*f", precision, ratePerBlockUsdNormalized), nil

	default:
		return "", fmt.Errorf("denom is not supported: %s", denom)
	}
}

// This function parses a string of GPU model to price mappings and returns a map
func parseGPUPriceMappings(mappingStr string) (map[string]float64, error) {
	gpuMappings := make(map[string]float64)

	// Return an empty map if the input string is empty, avoiding an error
	if mappingStr == "" {
		return gpuMappings, nil
	}

	pairs := strings.Split(mappingStr, ",")
	for _, pair := range pairs {
		// Continue with the next iteration if the pair is empty
		if pair == "" {
			continue
		}
		kv := strings.Split(pair, "=")
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid GPU mapping: %s", pair)
		}

		key := kv[0]
		value, err := strconv.ParseFloat(kv[1], 64)
		if err != nil {
			return nil, fmt.Errorf("invalid GPU price for %s: %v", key, err)
		}

		gpuMappings[key] = value
	}

	return gpuMappings, nil
}

// Assuming gpuMappings is already populated
func maxGPUPrice(gpuMappings map[string]float64) float64 {
	maxPrice := 100.0 // Default value
	for _, price := range gpuMappings {
		if price > maxPrice {
			maxPrice = price
		}
	}
	return maxPrice
}

func calculateTotalGPUPrice(gSpec *dtypes.GroupSpec, gpuMappings map[string]float64, maxGPUPrice float64) float64 {
	totalGPUPrice := 0.0

	for _, resourceUnit := range gSpec.Resources {
		count := float64(resourceUnit.Count)

		if resourceUnit.Resources.GPU != nil {
			gpuUnits := float64(resourceUnit.Resources.GPU.Units.Val.Int64())

			model := ""
			vram := ""
			for _, attr := range resourceUnit.Resources.GPU.Attributes {
				if attr.Key == "model" {
					model = attr.Value
				} else if attr.Key == "vram" {
					vram = attr.Value
				}
			}

			// Construct the key for price lookup
			gpuKey := model
			if vram != "" {
				gpuKey = fmt.Sprintf("%s.%s", model, vram)
			}

			price := maxGPUPrice // Default to max price if specific GPU price is not found
			if val, exists := gpuMappings[gpuKey]; exists {
				price = val
			}

			totalGPUPrice += count * gpuUnits * price
		}
	}

	return totalGPUPrice
}

// RequestToBidPrice is the entry point to execute the bidding logic.
func RequestToBidPrice(request Request) error {
	fmt.Println("####Request: ", request)
	owner := request.Owner
	if owner == "" {
		return fmt.Errorf("request owner is not specified")
	}

	var denom string
	var amount sdk.Dec
	if request.GSpec != nil && len(request.GSpec.Resources) > 0 {
		denom = request.GSpec.Resources[0].Price.Denom
		amount = request.GSpec.Resources[0].Price.Amount
	}

	if SpecialPricing(owner) {
		log.Println("Special pricing activated")
		specialRate := "1.00"
		fmt.Printf("Special pricing rate per block (uakt): %s\n", specialRate)
		return nil
	}

	if err := checkWhitelist(owner); err != nil {
		log.Printf("Whitelist check failed: %v", err)
		return fmt.Errorf("whitelist check failed: %v", err)
	}

	usdPerAkt, err := getAKTPrice()
	if err != nil {
		log.Printf("Error getting AKT price: %v", err)
		return fmt.Errorf("error getting AKT price: %v", err)
	}

	if denom == "" || amount.IsZero() {
		fmt.Println("Price information is missing or incomplete")
		return fmt.Errorf("price information is missing or incomplete")
	}

	precision := request.PricePrecision
	if precision == 0 {
		precision = 6
	}

	if request.GSpec == nil {
		return fmt.Errorf("GroupSpec is nil in the request")
	}

	priceTargets := setPriceTargets()
	maxGPUPrice := maxGPUPrice(priceTargets.GPUMappings)
	totalGPUPrice := calculateTotalGPUPrice(request.GSpec, priceTargets.GPUMappings, maxGPUPrice)
	resourceRequests := calculateRequestedResources(request.GSpec)
	totalCostUsdTarget := calculateTotalCostUsdTarget(resourceRequests, priceTargets) + totalGPUPrice

	// In RequestToBidPrice function
	_, _, finalRateStr := calculateBlockRates(totalCostUsdTarget, usdPerAkt, precision)

	if err != nil {
		log.Println(err)
		return err
	}

	// Now, finalRateStr already has the "uakt" suffix and the correct number of decimal places
	fmt.Printf("Total cost per block (uakt, formatted): %s\n", finalRateStr)

	fmt.Printf("Total cost in USD: %.2f/month\n", totalCostUsdTarget)

	return nil
}
