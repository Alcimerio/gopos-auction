package auction

import (
	"context"
	"fullcycle-auction_go/internal/entity/auction_entity"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func TestAuctionAutoClose(t *testing.T) {
	// Set a short auction interval for the test
	os.Setenv("AUCTION_INTERVAL", "1s")
	
	ctx := context.Background()
	
	username := "admin"
	password := "admin"
	host := "localhost"
	port := "27017"
	
	mongoURI := "mongodb://" + username + ":" + password + "@" + host + ":" + port + "/?authSource=admin"
	
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(mongoURI))
	if err != nil {
		t.Fatalf("Failed to connect to MongoDB: %v", err)
	}
	defer client.Disconnect(ctx)
	
	err = client.Ping(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to ping MongoDB: %v\nCheck if MongoDB is running and credentials are correct", err)
	}
	
	dbName := "test_auction_" + time.Now().Format("20060102150405")
	db := client.Database(dbName)
	defer db.Drop(ctx)
	
	repo := NewAuctionRepository(db)
	
	auction, err := auction_entity.CreateAuction(
		"Test Product",
		"Test Category",
		"This is a test description for the auto-close feature",
		auction_entity.New,
	)
	assert.Nil(t, err)
	
	err = repo.CreateAuction(ctx, auction)
	assert.Nil(t, err)
	
	var auctionDoc bson.M
	err = db.Collection("auctions").FindOne(ctx, bson.M{"_id": auction.Id}).Decode(&auctionDoc)
	assert.Nil(t, err)
	assert.Equal(t, int32(auction_entity.Active), auctionDoc["status"])
	
	// Wait for the auction interval to let the auto-close process work
	time.Sleep(2500 * time.Millisecond)
	
	// Check if the auction status was updated to Completed
	err = db.Collection("auctions").FindOne(ctx, bson.M{"_id": auction.Id}).Decode(&auctionDoc)
	assert.Nil(t, err)
	assert.Equal(t, int32(auction_entity.Completed), auctionDoc["status"], "Auction should be automatically closed after expiration")
	
	// Verify the auction is no longer in the active auctions map
	repo.mutex.RLock()
	_, exists := repo.activeAuctions[auction.Id]
	repo.mutex.RUnlock()
	assert.False(t, exists, "Auction should be removed from active auctions map after being closed")
}
