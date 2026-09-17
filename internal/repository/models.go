package repository

import "time"

type Image struct {
	ID           string
	UUID         string
	SHA1         string
	Folder       string
	Name         string
	OriginalName string
	MIMEType     string
	Size         int64
	UploadDate   time.Time
	UploadIP     string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type ImageCache struct {
	ID          string
	ImageID     string
	OriginalURL string
	Folder      string
	Name        string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type AccessRecord struct {
	ID          string
	ImageID     string
	IP          string
	OriginalURL string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type BingImage struct {
	ID            string
	StartDate     *string
	FullStartDate *string
	EndDate       *string
	URL           string
	URLBase       *string
	Copyright     *string
	CopyrightLink *string
	Quiz          *string
	WP            bool
	Hash          *string
	Dark          *int64
	Top           *int64
	Bottom        *int64
	Hotspots      []string
	File          *string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

type DownloadRecord struct {
	ID          string
	File        string
	IP          string
	OriginalURL string
	UserAgent   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}
