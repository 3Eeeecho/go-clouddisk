package setup

import (
	"github.com/3Eeeecho/go-clouddisk/internal/config"
	"github.com/3Eeeecho/go-clouddisk/internal/pkg/logger"
	"github.com/elastic/go-elasticsearch/v8"
	"go.uber.org/zap"
)

var EsClient *elasticsearch.Client

func InitElasticsearchClient(cfg *config.ElasticsearchConfig) {
	esCfg := elasticsearch.Config{
		Addresses: cfg.Addresses,
		Username:  cfg.Username,
		Password:  cfg.Password,
		// CloudID:   cfg.CloudID,
		// APIKey:    cfg.APIKey,
	}

	var err error
	if EsClient, err = elasticsearch.NewClient(esCfg); err != nil {
		logger.Fatal("Failed to create Elasticsearch client", zap.Error(err))
	}

	// 尝试连接并获取集群信息，验证连接是否成功
	res, err := EsClient.Info()
	if err != nil {
		logger.Fatal("Failed to connect to Elasticsearch", zap.Error(err))
	}
	defer res.Body.Close()

	if res.IsError() {
		logger.Fatal("Error connecting to Elasticsearch", zap.String("status", res.Status()), zap.Any("response", res.String()))
	}

	logger.Info("Elasticsearch client initialized successfully.", zap.String("cluster_name", res.String()))
}
