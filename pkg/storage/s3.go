package storage

import (
	"fmt"

	"github.com/2025-softbank-hackathon-final-navy/execution/config"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
)

var (
	s3Session *session.Session
	downloader *s3manager.Downloader
)

// InitS3Client initializes the S3 session and downloader.
func InitS3Client() error {
	var err error
	s3Session, err = session.NewSession(&aws.Config{
		Region: aws.String(config.AppConfig.AWSRegion),
	})
	if err != nil {
		return fmt.Errorf("failed to create new S3 session: %w", err)
	}

	downloader = s3manager.NewDownloader(s3Session)
	return nil
}

// DownloadCode downloads the function code from S3.
// The S3 key is constructed from the functionId and runtime.
func DownloadCode(functionId, runtime string) (string, error) {
	extension, err := getExtensionForRuntime(runtime)
	if err != nil {
		return "", err
	}
	s3Key := fmt.Sprintf("functions/%s%s", functionId, extension)

	buf := &aws.WriteAtBuffer{}
	_, err = downloader.Download(buf, &s3.GetObjectInput{
		Bucket: aws.String(config.AppConfig.S3Bucket),
		Key:    aws.String(s3Key),
	})
	if err != nil {
		return "", fmt.Errorf("failed to download file from S3 (bucket: %s, key: %s): %w", config.AppConfig.S3Bucket, s3Key, err)
	}

	return string(buf.Bytes()), nil
}

func getExtensionForRuntime(runtime string) (string, error) {
	switch runtime {
	case "python":
		return ".py", nil
	// TODO: Add other runtimes here, e.g., "nodejs" -> ".js"
	default:
		return "", fmt.Errorf("unsupported runtime: %s", runtime)
	}
}
