package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	mapset "github.com/deckarep/golang-set/v2"
)

const remoteCheckInterval = time.Duration(1 * time.Hour)

var supportedExt = mapset.NewSet(
	".jpeg", ".jpg", ".JPEG", ".JPG",
	".png", ".PNG",
)

type RemoteManager struct {
	client *s3.Client

	profile  string
	s3Bucket string

	outputPath string
}

func NewRemoteManager() (*RemoteManager, error) {
	// if empty then defaults to current directory
	outputPath := os.Getenv("DPF_S3_OUTPUT_PATH")
	if outputPath == "" {
		outputPath = "."
	}

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

	return &RemoteManager{
		client:     client,
		profile:    awsProfileName,
		s3Bucket:   s3Bucket,
		outputPath: outputPath,
	}, nil
}

func (a *RemoteManager) GetS3Objects(ctx context.Context) ([]s3types.Object, error) {
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

func (a *RemoteManager) DownloadObject(ctx context.Context, name string) error {
	downloader := manager.NewDownloader(a.client)

	f, err := os.Create(filepath.Join(a.outputPath, name))
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

func (a *RemoteManager) getLocalFiles() (mapset.Set[string], error) {
	dirs, err := os.ReadDir(a.outputPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read directory, %s, %w", a.outputPath, err)
	}

	localFiles := mapset.NewSet[string]()
	for dir := range slices.Values(dirs) {
		name := dir.Name()
		if !supportedExt.Contains(filepath.Ext(name)) {
			continue
		}
		localFiles.Add(name)
	}

	if localFiles.Cardinality() == 0 {
		slog.Info("no local files found")
	}
	return localFiles, nil
}

func (a *RemoteManager) getRemoteFiles(ctx context.Context) (mapset.Set[string], error) {
	remoteFiles := mapset.NewSet[string]()
	objects, err := a.GetS3Objects(ctx)
	if err != nil {
		return nil, err
	}
	for object := range slices.Values(objects) {
		name := aws.ToString(object.Key)
		if !supportedExt.Contains(filepath.Ext(name)) {
			continue
		}
		remoteFiles.Add(name)
	}

	if remoteFiles.Cardinality() == 0 {
		slog.Info("no remote files found")
	}
	return remoteFiles, nil
}

func (a *RemoteManager) SyncFolder(ctx context.Context) error {
	localFiles, err := a.getLocalFiles()
	if err != nil {
		return err
	}

	remoteFiles, err := a.getRemoteFiles(ctx)
	if err != nil {
		return err
	}

	toDelete := localFiles.Difference(remoteFiles).ToSlice()
	toDownload := remoteFiles.Difference(localFiles).ToSlice()
	if len(toDelete) == 0 && len(toDownload) == 0 {
		slog.Info("local and remote are in sync")
		return nil
	}
	if len(toDelete) > 0 {
		slog.Info("deleting local files", "count", len(toDelete), "names", toDelete)
		for name := range slices.Values(toDelete) {
			if err := os.Remove(name); err != nil {
				slog.Warn("unable to remove local file", "error", err)
			}
		}
	}
	if len(toDownload) > 0 {
		slog.Info("adding files", "count", len(toDownload), "names", toDownload)
		for name := range slices.Values(toDownload) {
			err := a.DownloadObject(ctx, name)
			if err != nil {
				slog.Warn("error while downloading s3 object", "name", name, "error", err)
			}
		}
	}
	return nil
}

func (m *RemoteManager) Run() {
	ticker := time.NewTicker(remoteCheckInterval)
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), time.Duration(30*time.Minute))
		if err := m.SyncFolder(ctx); err != nil {
			slog.Warn("error while syncing with remote", "error", err)
		}
		cancel()
	}
}

func main() {
	manager, err := NewRemoteManager()
	if err != nil {
		log.Fatal(err)
	}

	manager.Run()
}
