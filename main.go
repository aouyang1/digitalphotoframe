package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type AWSClient struct {
	client *s3.Client

	profile  string
	s3Bucket string

	outputPath string
}

func NewAWSClient() (*AWSClient, error) {
	// if empty then defaults to current directory
	outputPath := os.Getenv("DPF_S3_OUTPUT_PATH")

	awsProfileName := os.Getenv("DPF_AWS_PROFILE")
	if awsProfileName == "" {
		return nil, errors.New("no aws profile provided in environment variable DPF_AWS_PROFILE")
	}
	s3Bucket := os.Getenv("DPF_S3_BUCKET")
	if s3Bucket == "" {
		return nil, errors.New("no s3 bucket provided in environment variable DPF_S3_BUCKET")
	}

	// Load the Shared AWS Configuration (~/.aws/config)
	ctxCfg, cancelCfg := context.WithTimeout(context.Background(), time.Duration(3*time.Second))
	cfg, err := config.LoadDefaultConfig(
		ctxCfg,
		config.WithSharedConfigProfile(awsProfileName),
	)
	cancelCfg()
	if err != nil {
		return nil, err
	}

	// Create an Amazon S3 service client
	client := s3.NewFromConfig(cfg)

	return &AWSClient{
		client:     client,
		profile:    awsProfileName,
		s3Bucket:   s3Bucket,
		outputPath: outputPath,
	}, nil
}

func (a *AWSClient) GetS3Objects(ctx context.Context) ([]s3types.Object, error) {
	// Get the first page of results for ListObjectsV2 for a bucket
	output, err := a.client.ListObjectsV2(
		ctx,
		&s3.ListObjectsV2Input{
			Bucket: aws.String(a.s3Bucket),
		},
	)
	if err != nil {
		return nil, err
	}

	return output.Contents, nil
}

func (a *AWSClient) DownloadObject(ctx context.Context, name string) error {
	downloader := manager.NewDownloader(a.client)

	f, err := os.Create(name)
	if err != nil {
		return fmt.Errorf("unable to create file for s3 download, %s, %w", name, err)
	}
	defer f.Close()

	if _, err := downloader.Download(ctx, f, &s3.GetObjectInput{
		Bucket: aws.String(a.s3Bucket),
		Key:    aws.String(name),
	}); err != nil {
		return fmt.Errorf("unable to download object from s3, %s, %w", name, err)
	}
	return nil
}

func main() {
	client, err := NewAWSClient()
	if err != nil {
		log.Fatal(err)
	}

	ctxListObj, cancelListObj := context.WithTimeout(context.Background(), time.Duration(10*time.Second))
	defer cancelListObj()
	objects, err := client.GetS3Objects(ctxListObj)
	if err != nil {
		log.Fatal(err)
	}
	for _, object := range objects {
		name := aws.ToString(object.Key)
		fmt.Println(name)
		ctxDownload, cancelDownload := context.WithTimeout(context.Background(), time.Duration(10*time.Second))
		err := client.DownloadObject(ctxDownload, name)
		cancelDownload()
		if err != nil {
			slog.Warn("error while downloading s3 object", "name", name, "error", err)
		}
	}
}
