package imageproxy

import (
	"io"
	"image/gif"
	"image"
	"image/draw"
	"image/color"
)

// TransformFunc is a function that transforms an image.
type TransformFunc func(image.Image) image.Image

// Process the GIF read from r, applying transform to each frame, and writing
// the result to w.
func GifProcess(w io.Writer, r io.Reader, transform TransformFunc) error {
	if transform == nil {
		_, err := io.Copy(w, r)
		return err
	}

	// Decode the original gif.
	im, err := gif.DecodeAll(r)
	if err != nil {
		return err
	}

	// Create a new RGBA image to hold the incremental frames.
	firstFrame := im.Image[0].Bounds()
	b := image.Rect(0, 0, firstFrame.Dx(), firstFrame.Dy())
	img := image.NewRGBA(b)

	// Resize each frame.
	for index, frame := range im.Image {
		bounds := frame.Bounds()
		previous := img
		draw.Draw(img, bounds, frame, bounds.Min, draw.Over)
		im.Image[index] = imageToPaletted(transform(img), frame.Palette)

		switch im.Disposal[index] {
		case gif.DisposalBackground:
			// I'm just assuming that the gif package will apply the appropriate
			// background here, since there doesn't seem to be an easy way to
			// access the global color table
			img = image.NewRGBA(b)
		case gif.DisposalPrevious:
			img = previous
		}
	}

	// Set image.Config to new height and width
	im.Config.Width = im.Image[0].Bounds().Max.X
	im.Config.Height = im.Image[0].Bounds().Max.Y

	return gif.EncodeAll(w, im)
}

func imageToPaletted(img image.Image, p color.Palette) *image.Paletted {
	b := img.Bounds()
	pm := image.NewPaletted(b, p)
	draw.FloydSteinberg.Draw(pm, b, img, image.ZP)
	return pm
}