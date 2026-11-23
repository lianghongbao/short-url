package repository

import (
	"context"
	"fmt"
	"time"

	"short-url/internal/model"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoRepository struct {
	coll *mongo.Collection
}

func NewMongoRepository(coll *mongo.Collection) *MongoRepository {
	return &MongoRepository{coll: coll}
}

func (r *MongoRepository) EnsureIndexes(ctx context.Context) error {
	indexes := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "short_code", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys:    bson.D{{Key: "long_url", Value: 1}},
			Options: options.Index().SetUnique(true),
		},
		{
			Keys: bson.D{{Key: "status", Value: 1}, {Key: "expire_at", Value: 1}},
		},
	}

	_, err := r.coll.Indexes().CreateMany(ctx, indexes)
	return err
}

func (r *MongoRepository) Create(ctx context.Context, url *model.URL) error {
	url.CreatedAt = time.Now().UTC()
	url.LastVisited = time.Now().UTC()
	url.ClickCount = 0

	_, err := r.coll.InsertOne(ctx, url)
	if mongo.IsDuplicateKeyError(err) {
		return ErrDuplicate
	}
	return err
}

func (r *MongoRepository) FindByCode(ctx context.Context, code string) (*model.URL, error) {
	var url model.URL
	err := r.coll.FindOne(ctx, bson.M{"short_code": code}).Decode(&url)
	if err == mongo.ErrNoDocuments {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("按短码查询失败: %w", err)
	}
	return &url, nil
}

func (r *MongoRepository) FindByLongURL(ctx context.Context, longURL string) (*model.URL, error) {
	var url model.URL
	err := r.coll.FindOne(ctx, bson.M{"long_url": longURL}).Decode(&url)
	if err == mongo.ErrNoDocuments {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("按长链接查询失败: %w", err)
	}
	return &url, nil
}

func (r *MongoRepository) IncrClickCount(ctx context.Context, code string, delta int64) error {
	_, err := r.coll.UpdateOne(
		ctx,
		bson.M{"short_code": code},
		bson.M{
			"$inc": bson.M{"click_count": delta},
			"$set": bson.M{"last_visited": time.Now().UTC()},
		},
	)
	return err
}

var (
	ErrNotFound  = fmt.Errorf("记录不存在")
	ErrDuplicate = fmt.Errorf("重复键冲突")
)
