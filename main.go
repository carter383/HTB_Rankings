package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
)

var (
	// in‑memory cache and its mutex
	dataCache    map[string]interface{}
	cacheMutex   sync.RWMutex
	dynamoClient *dynamodb.Client
	awsRegion    string
)

func init() {
	// load AWS config once (reads AWS_REGION env var, profile, etc.)
	cfg, err := awsconfig.LoadDefaultConfig(context.Background())
	if err != nil {
		log.Fatalf("unable to load AWS SDK config: %v", err)
	}
	awsRegion = cfg.Region
	dynamoClient = dynamodb.NewFromConfig(cfg)
	dataCache = make(map[string]interface{})
}

func handler(ctx context.Context) (map[string]interface{}, error) {
	// return cached if present
	cacheMutex.RLock()
	if len(dataCache) != 0 {
		res := make(map[string]interface{}, len(dataCache))
		for k, v := range dataCache {
			res[k] = v
		}
		cacheMutex.RUnlock()
		return res, nil
	}
	cacheMutex.RUnlock()

	// today’s date key
	today := time.Now().Format("2006-01-02")

	// table name from env
	tableName := os.Getenv("TABLE_NAME")
	if tableName == "" {
		return map[string]interface{}{"error": "TABLE_NAME not configured"}, nil
	}

	// attempt to read from DynamoDB
	getResp, err := dynamoClient.GetItem(ctx, &dynamodb.GetItemInput{
		TableName: aws.String(tableName),
		Key: map[string]types.AttributeValue{
			"date": &types.AttributeValueMemberS{Value: today},
		},
	})
	if err != nil {
		log.Printf("⛔ GetItem failed (region=%s, table=%s, key=%s): %v",
			awsRegion, tableName, today, err)
		return map[string]interface{}{
			"error":  "Database lookup failed",
			"detail": err.Error(),
		}, nil
	}
	if getResp.Item != nil {
		var item map[string]interface{}
		if err := attributevalue.UnmarshalMap(getResp.Item, &item); err == nil {
			cacheMutex.Lock()
			dataCache = item
			cacheMutex.Unlock()
			return item, nil
		}
	}

	// no existing item → fetch from HTB API
	info, err := getRankingsFromHTB(ctx)
	if err != nil {
		// write an empty item so we don’t hammer the API
		_, _ = dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
			TableName: aws.String(tableName),
			Item: map[string]types.AttributeValue{
				"date": &types.AttributeValueMemberS{Value: today},
			},
		})
		return map[string]interface{}{"error": err.Error()}, nil
	}

	// prepare full item for DynamoDB
	itemToStore := map[string]interface{}{"date": today}
	for k, v := range info {
		itemToStore[k] = v
	}
	av, err := attributevalue.MarshalMap(itemToStore)
	if err != nil {
		return map[string]interface{}{"error": "Error marshalling item"}, nil
	}

	// write to DynamoDB
	if _, err := dynamoClient.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: aws.String(tableName),
		Item:      av,
	}); err != nil {
		log.Printf("⛔ PutItem failed (region=%s, table=%s, key=%s): %v",
			awsRegion, tableName, today, err)
		return map[string]interface{}{
			"error":  "Error writing item to DynamoDB",
			"detail": err.Error(),
		}, nil
	}

	// update cache and return
	cacheMutex.Lock()
	dataCache = info
	cacheMutex.Unlock()
	return info, nil
}

func getRankingsFromHTB(ctx context.Context) (map[string]interface{}, error) {
	userID := os.Getenv("USER_ID")
	appToken := os.Getenv("TOKEN")
	if userID == "" || appToken == "" {
		return nil, errors.New("USER_ID or TOKEN not configured")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	headers := map[string]string{
		"Authorization": "Bearer " + appToken,
		"User-Agent":    "Mozilla/5.0 (Windows NT 10.0; Win64; x64)",
	}

	doGet := func(url string, target interface{}) error {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return errors.New("non-200 response")
		}
		return json.NewDecoder(resp.Body).Decode(target)
	}

	// 1) basic profile
	var profileResp struct {
		Profile struct {
			Name         string `json:"name"`
			CountryCode  string `json:"country_code"`
			SystemOwns   int    `json:"system_owns"`  // now plain int
			UserOwns     int    `json:"user_owns"`
			SystemBloods int    `json:"system_bloods"`
			UserBloods   int    `json:"user_bloods"`
			Rank         string `json:"rank"`         // kept as string
			Ranking      int    `json:"ranking"`
		} `json:"profile"`
	}
	if err := doGet("https://labs.hackthebox.com/api/v4/user/profile/basic/"+userID, &profileResp); err != nil {
		return nil, err
	}
	name := profileResp.Profile.Name
	code := profileResp.Profile.CountryCode
	if name == "" || code == "" {
		return nil, errors.New("Could not retrieve user profile")
	}

	info := map[string]interface{}{
		"System_Owns":      profileResp.Profile.SystemOwns,
		"User_Owns":        profileResp.Profile.UserOwns,
		"System_Bloods":    profileResp.Profile.SystemBloods,
		"User_Bloods":      profileResp.Profile.UserBloods,
		"Rank":             profileResp.Profile.Rank,
		"User_Global_Rank": profileResp.Profile.Ranking,
	}

	// 2) local rankings
	var localResp struct {
		Data struct {
			Rankings []struct {
				Name string `json:"name"`
				Rank int    `json:"rank"`  // plain int
			} `json:"rankings"`
		} `json:"data"`
	}
	_ = doGet("https://labs.hackthebox.com/api/v4/rankings/country/"+code+"/members", &localResp)
	for _, r := range localResp.Data.Rankings {
		if r.Name == name {
			info["Local_Rank"] = r.Rank
			break
		}
	}

	// 3) challenge progress
	var challResp struct {
		Profile struct {
			ChallengeOwns struct {
				Solved int `json:"solved"`  // plain int
			} `json:"challenge_owns"`
		} `json:"profile"`
	}
	_ = doGet("https://labs.hackthebox.com/api/v4/user/profile/progress/challenges/"+userID, &challResp)
	info["Challenge_Owns"] = challResp.Profile.ChallengeOwns.Solved

	return info, nil
}

func main() {
	lambda.Start(handler)
}
