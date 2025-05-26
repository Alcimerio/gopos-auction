package auction

import (
	"context"
	"fmt"
	"fullcycle-auction_go/configuration/logger"
	"fullcycle-auction_go/internal/entity/auction_entity"
	"fullcycle-auction_go/internal/internal_error"
	"os"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type AuctionEntityMongo struct {
	Id          string                          `bson:"_id"`
	ProductName string                          `bson:"product_name"`
	Category    string                          `bson:"category"`
	Description string                          `bson:"description"`
	Condition   auction_entity.ProductCondition `bson:"condition"`
	Status      auction_entity.AuctionStatus    `bson:"status"`
	Timestamp   int64                           `bson:"timestamp"`
}
type AuctionRepository struct {
	Collection *mongo.Collection
	auctionInterval time.Duration
	activeAuctions map[string]time.Time
	mutex *sync.RWMutex
	closingWorkerStarted bool
}

func NewAuctionRepository(database *mongo.Database) *AuctionRepository {
	repo := &AuctionRepository{
		Collection: database.Collection("auctions"),
		auctionInterval: getAuctionInterval(),
		activeAuctions: make(map[string]time.Time),
		mutex: &sync.RWMutex{},
		closingWorkerStarted: false,
	}
	
	go repo.startClosingWorker()
	
	return repo
}

// getAuctionInterval returns the auction duration from environment variable
func getAuctionInterval() time.Duration {
	auctionInterval := os.Getenv("AUCTION_INTERVAL")
	duration, err := time.ParseDuration(auctionInterval)
	if err != nil {
		return time.Minute * 5
	}
	return duration
}

// startClosingWorker starts a goroutine that periodically checks for expired auctions
func (ar *AuctionRepository) startClosingWorker() {
	if ar.closingWorkerStarted {
		return
	}
	
	ar.closingWorkerStarted = true
	checkInterval := time.Second * 1 // Check every 1 second (for faster testing)
	
	ticker := time.NewTicker(checkInterval)
	
	// Important: don't use defer ticker.Stop() in a goroutine
	go func() {
		for {
			select {
			case <-ticker.C:
				ar.closeExpiredAuctions()
			}
		}
	}()
}

// closeExpiredAuctions checks and closes all expired auctions
func (ar *AuctionRepository) closeExpiredAuctions() {
	now := time.Now()
	expiredAuctionIDs := []string{}
	
	// First identify expired auctions
	ar.mutex.RLock()
	for auctionID, endTime := range ar.activeAuctions {
		if now.After(endTime) {
			expiredAuctionIDs = append(expiredAuctionIDs, auctionID)
		}
	}
	ar.mutex.RUnlock()
	
	logger.Info("Checking for expired auctions: found " + fmt.Sprintf("%d", len(expiredAuctionIDs)))
	
	// Then close each expired auction
	for _, auctionID := range expiredAuctionIDs {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		err := ar.closeAuction(ctx, auctionID)
		cancel()
		
		// Only remove from active auctions if successful
		if err == nil {
			ar.mutex.Lock()
			delete(ar.activeAuctions, auctionID)
			ar.mutex.Unlock()
			logger.Info("Auction removed from tracking: " + auctionID)
		}
	}
}

// closeAuction updates the auction status to Completed
func (ar *AuctionRepository) closeAuction(ctx context.Context, auctionID string) error {
	filter := bson.M{"_id": auctionID}
	update := bson.M{"$set": bson.M{"status": auction_entity.Completed}}
	
	_, err := ar.Collection.UpdateOne(ctx, filter, update)
	if err != nil {
		logger.Error("Error closing expired auction", err)
		return err
	}

	logger.Info("Auction closed successfully: " + auctionID)
	return nil
}

func (ar *AuctionRepository) CreateAuction(
	ctx context.Context,
	auctionEntity *auction_entity.Auction) *internal_error.InternalError {
	auctionEntityMongo := &AuctionEntityMongo{
		Id:          auctionEntity.Id,
		ProductName: auctionEntity.ProductName,
		Category:    auctionEntity.Category,
		Description: auctionEntity.Description,
		Condition:   auctionEntity.Condition,
		Status:      auctionEntity.Status,
		Timestamp:   auctionEntity.Timestamp.Unix(),
	}
	_, err := ar.Collection.InsertOne(ctx, auctionEntityMongo)
	if err != nil {
		logger.Error("Error trying to insert auction", err)
		return internal_error.NewInternalServerError("Error trying to insert auction")
	}

	// Register this auction to be tracked for auto-closing
	endTime := auctionEntity.Timestamp.Add(ar.auctionInterval)
	ar.mutex.Lock()
	ar.activeAuctions[auctionEntity.Id] = endTime
	ar.mutex.Unlock()

	logger.Info("Auction created and scheduled to close at: " + endTime.String())
	return nil
}
