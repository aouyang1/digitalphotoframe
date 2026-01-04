package templates

import (
	"fmt"
	"net/url"

	"github.com/aouyang1/digitalphotoframe/store"
)

func photoImageURL(photo store.Photo) string {
	encodedName := url.PathEscape(photo.PhotoName)
	return fmt.Sprintf("/photos/%d/%s/image", photo.Category, encodedName)
}

func playImageURL(photo store.Photo) string {
	return fmt.Sprintf("/slideshow/play/%s/category/%d", url.PathEscape(photo.PhotoName), photo.Category)
}

func deleteURL(photo store.Photo) string {
	encodedName := url.PathEscape(photo.PhotoName)
	return fmt.Sprintf("/photos/%s/category/%d", encodedName, photo.Category)
}
