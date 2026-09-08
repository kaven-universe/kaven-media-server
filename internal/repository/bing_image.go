package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

type BingImageRepository struct {
	db *sql.DB
}

func NewBingImageRepository(db *sql.DB) *BingImageRepository {
	return &BingImageRepository{db: db}
}

func encodeHotspots(image BingImage) (string, error) {
	hotspots := image.Hotspots
	if hotspots == nil {
		hotspots = []string{}
	}
	hotspotsJSON, err := json.Marshal(hotspots)
	if err != nil {
		return "", fmt.Errorf("encode Bing image hotspots: %w", err)
	}
	return string(hotspotsJSON), nil
}

func (repository *BingImageRepository) Create(ctx context.Context, image BingImage) error {
	hotspotsJSON, err := encodeHotspots(image)
	if err != nil {
		return err
	}
	_, err = repository.db.ExecContext(ctx, `
		INSERT INTO bing_images (
			id, start_date, full_start_date, end_date, url, url_base,
			copyright, copyright_link, quiz, wp, hsh, drk, top, bot,
			hs_json, file, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		image.ID, image.StartDate, image.FullStartDate, image.EndDate,
		image.URL, image.URLBase, image.Copyright, image.CopyrightLink,
		image.Quiz, image.WP, image.Hash, image.Dark, image.Top, image.Bottom,
		hotspotsJSON, image.File, formatTimestamp(image.CreatedAt),
		formatTimestamp(image.UpdatedAt),
	)
	if err != nil {
		return writeError("create Bing image", err)
	}
	return nil
}

func (repository *BingImageRepository) Upsert(ctx context.Context, image BingImage) error {
	hotspotsJSON, err := encodeHotspots(image)
	if err != nil {
		return err
	}

	_, err = repository.db.ExecContext(ctx, `
		INSERT INTO bing_images (
			id, start_date, full_start_date, end_date, url, url_base,
			copyright, copyright_link, quiz, wp, hsh, drk, top, bot,
			hs_json, file, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET
			start_date = excluded.start_date,
			full_start_date = excluded.full_start_date,
			end_date = excluded.end_date,
			url_base = excluded.url_base,
			copyright = excluded.copyright,
			copyright_link = excluded.copyright_link,
			quiz = excluded.quiz,
			wp = excluded.wp,
			hsh = excluded.hsh,
			drk = excluded.drk,
			top = excluded.top,
			bot = excluded.bot,
			hs_json = excluded.hs_json,
			file = excluded.file,
			updated_at = excluded.updated_at`,
		image.ID, image.StartDate, image.FullStartDate, image.EndDate,
		image.URL, image.URLBase, image.Copyright, image.CopyrightLink,
		image.Quiz, image.WP, image.Hash, image.Dark, image.Top, image.Bottom,
		hotspotsJSON, image.File, formatTimestamp(image.CreatedAt),
		formatTimestamp(image.UpdatedAt),
	)
	if err != nil {
		return writeError("upsert Bing image", err)
	}
	return nil
}

func (repository *BingImageRepository) GetByID(ctx context.Context, id string) (BingImage, error) {
	image, err := scanBingImage(repository.db.QueryRowContext(ctx, bingImageSelect+" WHERE id = ?", id))
	if err != nil {
		return BingImage{}, notFoundError("get Bing image by ID", err)
	}
	return image, nil
}

func (repository *BingImageRepository) GetByURL(ctx context.Context, url string) (BingImage, error) {
	image, err := scanBingImage(repository.db.QueryRowContext(ctx, bingImageSelect+" WHERE url = ?", url))
	if err != nil {
		return BingImage{}, notFoundError("get Bing image by URL", err)
	}
	return image, nil
}

func (repository *BingImageRepository) RandomWithFile(ctx context.Context) (BingImage, error) {
	image, err := scanBingImage(repository.db.QueryRowContext(ctx, bingImageSelect+" WHERE file IS NOT NULL AND file <> '' ORDER BY random() LIMIT 1"))
	if err != nil {
		return BingImage{}, notFoundError("get random downloaded Bing image", err)
	}
	return image, nil
}

const bingImageSelect = `
	SELECT id, start_date, full_start_date, end_date, url, url_base,
		copyright, copyright_link, quiz, wp, hsh, drk, top, bot,
		hs_json, file, created_at, updated_at
	FROM bing_images`

func scanBingImage(scanner rowScanner) (BingImage, error) {
	var image BingImage
	var startDate, fullStartDate, endDate sql.NullString
	var urlBase, copyright, copyrightLink, quiz, hash, file sql.NullString
	var dark, top, bottom sql.NullInt64
	var hotspotsJSON, createdAt, updatedAt string
	var wallpaper bool
	if err := scanner.Scan(
		&image.ID, &startDate, &fullStartDate, &endDate, &image.URL,
		&urlBase, &copyright, &copyrightLink, &quiz, &wallpaper, &hash,
		&dark, &top, &bottom, &hotspotsJSON, &file, &createdAt, &updatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return BingImage{}, ErrNotFound
		}
		return BingImage{}, fmt.Errorf("scan Bing image: %w", err)
	}

	image.StartDate = stringPointer(startDate)
	image.FullStartDate = stringPointer(fullStartDate)
	image.EndDate = stringPointer(endDate)
	image.URLBase = stringPointer(urlBase)
	image.Copyright = stringPointer(copyright)
	image.CopyrightLink = stringPointer(copyrightLink)
	image.Quiz = stringPointer(quiz)
	image.WP = wallpaper
	image.Hash = stringPointer(hash)
	image.Dark = int64Pointer(dark)
	image.Top = int64Pointer(top)
	image.Bottom = int64Pointer(bottom)
	image.File = stringPointer(file)
	if err := json.Unmarshal([]byte(hotspotsJSON), &image.Hotspots); err != nil {
		return BingImage{}, fmt.Errorf("decode Bing image hotspots: %w", err)
	}

	var err error
	if image.CreatedAt, err = parseTimestamp("Bing image created_at", createdAt); err != nil {
		return BingImage{}, err
	}
	if image.UpdatedAt, err = parseTimestamp("Bing image updated_at", updatedAt); err != nil {
		return BingImage{}, err
	}
	return image, nil
}

func stringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func int64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}
