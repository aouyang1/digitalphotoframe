package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/aouyang1/digitalphotoframe/api/client"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	mapset "github.com/deckarep/golang-set/v2"
)

const remoteCheckInterval = time.Duration(1 * time.Hour)

type RemoteManager struct {
	client *s3.Client

	profile  string
	s3Bucket string

	outputPath string

	photoClient *client.PhotoClient

	Updated chan bool
}

func NewRemoteManager() (*RemoteManager, error) {
	// if empty then defaults to current directory
	rootPath := os.Getenv("DPF_ROOT_PATH")
	if rootPath == "" {
		rootPath = "."
	}
	outputPath := filepath.Join(rootPath, "original/surprise")

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
	s3Client := s3.NewFromConfig(cfg)

	// Initialize photo client if web server URL is available
	var photoClient *client.PhotoClient
	webServerURL := os.Getenv("DPF_WEBSERVER_URL")
	if webServerURL == "" {
		webServerURL = "http://localhost:80"
	}
	photoClient = client.NewPhotoClient(webServerURL)

	return &RemoteManager{
		client:      s3Client,
		profile:     awsProfileName,
		s3Bucket:    s3Bucket,
		outputPath:  outputPath,
		photoClient: photoClient,
		Updated:     make(chan bool),
	}, nil
}

func (r *RemoteManager) GetS3Objects(ctx context.Context) ([]s3types.Object, error) {
	// Get the first page of results for ListObjectsV2 for a bucket
	output, err := r.client.ListObjectsV2(
		ctx,
		&s3.ListObjectsV2Input{
			Bucket: aws.String(r.s3Bucket),
		},
	)
	if err != nil {
		return nil, err
	}

	return output.Contents, nil
}

func (r *RemoteManager) DownloadObject(ctx context.Context, name string) error {
	downloader := manager.NewDownloader(r.client)

	f, err := os.Create(filepath.Join(r.outputPath, name))
	if err != nil {
		return fmt.Errorf("unable to create file for s3 download, %s, %w", name, err)
	}
	defer f.Close()

	if _, err := downloader.Download(ctx, f, &s3.GetObjectInput{
		Bucket: aws.String(r.s3Bucket),
		Key:    aws.String(name),
	}); err != nil {
		return fmt.Errorf("unable to download object from s3, %s, %w", name, err)
	}
	return nil
}

func (r *RemoteManager) getLocalFiles() (mapset.Set[string], error) {
	dirs, err := os.ReadDir(r.outputPath)
	if err != nil {
		return nil, fmt.Errorf("unable to read directory, %s, %w", r.outputPath, err)
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

func (r *RemoteManager) getRemoteFiles(ctx context.Context) (mapset.Set[string], error) {
	remoteFiles := mapset.NewSet[string]()
	objects, err := r.GetS3Objects(ctx)
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

func (r *RemoteManager) SyncFolder(ctx context.Context) error {
	localFiles, err := r.getLocalFiles()
	if err != nil {
		return err
	}

	remoteFiles, err := r.getRemoteFiles(ctx)
	if err != nil {
		return err
	}

	toDelete := localFiles.Difference(remoteFiles).ToSlice()
	toDownload := remoteFiles.Difference(localFiles).ToSlice()
	if len(toDelete) > 0 {
		slog.Info("deleting local files", "count", len(toDelete), "names", toDelete)
		for name := range slices.Values(toDelete) {
			filePath := filepath.Join(r.outputPath, name)
			if err := os.Remove(filePath); err != nil {
				slog.Warn("unable to remove local file", "error", err)
			}
		}
	}
	if len(toDownload) > 0 {
		slog.Info("adding files", "count", len(toDownload), "names", toDownload)
		for name := range slices.Values(toDownload) {
			err := r.DownloadObject(ctx, name)
			if err != nil {
				slog.Warn("error while downloading s3 object", "name", name, "error", err)
				continue
			}

			// Register photo in database via web server
			photoPath := filepath.Join(r.outputPath, name)
			if err := r.photoClient.RegisterPhotoIfNotExists(photoPath, 0); err != nil {
				slog.Warn("error while registering photo", "name", name, "error", err)
				// Continue even if registration fails - file is downloaded
			}
		}
	}

	// After syncing with S3, ensure DB is in sync with local files for category 0
	// Get current local files again (in case they changed during sync)
	localFiles, err = r.getLocalFiles()
	if err != nil {
		slog.Warn("error getting local files for DB sync", "error", err)
	} else {
		// Ensure all local files are registered
		for _, name := range localFiles.ToSlice() {
			photoPath := filepath.Join(r.outputPath, name)
			if err := r.photoClient.RegisterPhotoIfNotExists(photoPath, 0); err != nil {
				slog.Warn("error while registering local photo", "name", name, "error", err)
			}
		}

		// Get all registered category 0 photos from DB
		registeredPhotos, err := r.photoClient.GetPhotos(0)
		if err != nil {
			slog.Warn("error getting registered photos from DB", "error", err)
		} else {
			// Create set of registered photo names
			registeredNames := mapset.NewSet[string]()
			for _, photo := range registeredPhotos {
				registeredNames.Add(photo.PhotoName)
			}

			// Find photos registered in DB but not present locally
			toDeregister := registeredNames.Difference(localFiles).ToSlice()
			if len(toDeregister) > 0 {
				slog.Info("deregistering photos not present locally", "count", len(toDeregister), "names", toDeregister)
				for _, name := range toDeregister {
					if err := r.photoClient.DeletePhoto(name, 0); err != nil {
						slog.Warn("error while deregistering photo", "name", name, "error", err)
					}
				}
			}
		}
	}

	// Only signal update if there were actual changes
	if len(toDelete) > 0 || len(toDownload) > 0 {
		r.Updated <- true
	}
	return nil
}

func (r *RemoteManager) Run() {
	ticker := time.NewTicker(remoteCheckInterval)

	// Initial sync
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(30*time.Minute))
	if err := r.SyncFolder(ctx); err != nil {
		slog.Warn("error while syncing with remote", "error", err)
	}
	cancel()

	for range ticker.C {
		ctx, cancel = context.WithTimeout(context.Background(), time.Duration(30*time.Minute))
		if err := r.SyncFolder(ctx); err != nil {
			slog.Warn("error while syncing with remote", "error", err)
		}
		cancel()
	}
}
