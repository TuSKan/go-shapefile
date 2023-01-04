// Package shapefile reads ESRI Shapefiles.
//
// See https://support.esri.com/en/white-paper/279.
package shapefile

// FIXME document all exported types
// FIXME make everything robust to malicious inputs
// FIXME fuzz testing
// FIXME tidy up errors
// FIXME provide lazy, random access to individual records, using SHX
// FIXME cross-file validation of offsets and record lengths

import (
	"archive/zip"
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/twpayne/go-geom"
)

const (
	headerSize = 100
	fileCode   = 9994
	version    = 1000
)

type SHxHeader struct {
	ShapeType ShapeType
	Bounds    *geom.Bounds
}

type ShapeType uint

// Shape types.
const (
	ShapeTypeNull        ShapeType = 0
	ShapeTypePoint       ShapeType = 1
	ShapeTypePolyLine    ShapeType = 3
	ShapeTypePolygon     ShapeType = 5
	ShapeTypeMultiPoint  ShapeType = 8
	ShapeTypePointZ      ShapeType = 11
	ShapeTypePolyLineZ   ShapeType = 13
	ShapeTypePolygonZ    ShapeType = 15
	ShapeTypeMultiPointZ ShapeType = 18
	ShapeTypePointM      ShapeType = 21
	ShapeTypePolyLineM   ShapeType = 23
	ShapeTypePolygonM    ShapeType = 25
	ShapeTypeMultiPointM ShapeType = 28
	ShapeTypeMultiPatch  ShapeType = 31
)

var knownShapeTypes = map[ShapeType]struct{}{
	ShapeTypeNull:        {},
	ShapeTypePoint:       {},
	ShapeTypePolyLine:    {},
	ShapeTypePolygon:     {},
	ShapeTypeMultiPoint:  {},
	ShapeTypePointM:      {},
	ShapeTypePolyLineM:   {},
	ShapeTypePolygonM:    {},
	ShapeTypeMultiPointM: {},
	ShapeTypePointZ:      {},
	ShapeTypePolyLineZ:   {},
	ShapeTypePolygonZ:    {},
	ShapeTypeMultiPointZ: {},
	// ShapeTypeMultiPatch:  {}, // FIXME
}

type Shapefile struct {
	DBF *DBF
	PRJ *PRJ
	SHP *SHP
	SHX *SHX
}

type ReadShapefileOptions struct {
	DBF *ReadDBFOptions
	SHP *ReadSHPOptions
}

func ReadFS(fsys fs.FS, basename string, options *ReadShapefileOptions) (*Shapefile, error) {
	var dbf *DBF
	switch dbfFile, err := fsys.Open(basename + ".dbf"); {
	case errors.Is(err, fs.ErrNotExist):
		// Do nothing.
	case err != nil:
		return nil, err
	default:
		defer dbfFile.Close()
		fileInfo, err := dbfFile.Stat()
		if err != nil {
			return nil, err
		}
		var readDBFOptions *ReadDBFOptions
		if options != nil {
			readDBFOptions = options.DBF
		}
		dbf, err = ReadDBF(dbfFile, fileInfo.Size(), readDBFOptions)
		if err != nil {
			return nil, err
		}
	}

	var prj *PRJ
	switch prjFile, err := fsys.Open(basename + ".prj"); {
	case errors.Is(err, fs.ErrNotExist):
		// Do nothing.
	case err != nil:
		return nil, err
	default:
		defer prjFile.Close()
		fileInfo, err := prjFile.Stat()
		if err != nil {
			return nil, err
		}
		prj, err = ReadPRJ(prjFile, fileInfo.Size())
		if err != nil {
			return nil, err
		}
	}

	var shp *SHP
	switch shpFile, err := fsys.Open(basename + ".shp"); {
	case errors.Is(err, fs.ErrNotExist):
		// Do nothing.
	case err != nil:
		return nil, err
	default:
		defer shpFile.Close()
		fileInfo, err := shpFile.Stat()
		if err != nil {
			return nil, err
		}
		var readSHPOptions *ReadSHPOptions
		if options != nil {
			readSHPOptions = options.SHP
		}
		shp, err = ReadSHP(shpFile, fileInfo.Size(), readSHPOptions)
		if err != nil {
			return nil, err
		}
	}

	var shx *SHX
	switch shxFile, err := fsys.Open(basename + ".shx"); {
	case errors.Is(err, fs.ErrNotExist):
		// Do nothing.
	case err != nil:
		return nil, err
	default:
		defer shxFile.Close()
		fileInfo, err := shxFile.Stat()
		if err != nil {
			return nil, err
		}
		shx, err = ReadSHX(shxFile, fileInfo.Size())
		if err != nil {
			return nil, err
		}
	}

	return &Shapefile{
		DBF: dbf,
		PRJ: prj,
		SHP: shp,
		SHX: shx,
	}, nil
}

func ReadZipFile(name string, options *ReadShapefileOptions) (*Shapefile, error) {
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}

	zipReader, err := zip.NewReader(file, fileInfo.Size())
	if err != nil {
		return nil, err
	}

	return ReadZipReader(zipReader, options)
}

func ReadZipReader(zipReader *zip.Reader, options *ReadShapefileOptions) (*Shapefile, error) {
	var dbfFiles []*zip.File
	var prjFiles []*zip.File
	var shxFiles []*zip.File
	var shpFiles []*zip.File
	for _, zipFile := range zipReader.File {
		switch strings.ToLower(filepath.Ext(zipFile.Name)) {
		case ".dbf":
			dbfFiles = append(dbfFiles, zipFile)
		case ".prj":
			prjFiles = append(prjFiles, zipFile)
		case ".shp":
			shpFiles = append(shpFiles, zipFile)
		case ".shx":
			shxFiles = append(shxFiles, zipFile)
		}
	}

	var dbf *DBF
	switch len(dbfFiles) {
	case 0:
		// Do nothing.
	case 1:
		var readDBFOptions *ReadDBFOptions
		if options != nil {
			readDBFOptions = options.DBF
		}
		var err error
		dbf, err = ReadDBFZipFile(dbfFiles[0], readDBFOptions)
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("multiple .dbf files") // FIXME
	}

	var prj *PRJ
	switch len(prjFiles) {
	case 0:
		// Do nothing.
	case 1:
		var err error
		prj, err = ReadPRJZipFile(prjFiles[0])
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("multiple .prj files") // FIXME
	}

	var shp *SHP
	switch len(shpFiles) {
	case 0:
		// Do nothing.
	case 1:
		var readSHPOptions *ReadSHPOptions
		if options != nil {
			readSHPOptions = options.SHP
		}
		var err error
		shp, err = ReadSHPZipFile(shpFiles[0], readSHPOptions)
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("multiple .shp files") // FIXME
	}

	var shx *SHX
	switch len(shxFiles) {
	case 0:
		// Do nothing.
	case 1:
		var err error
		shx, err = ReadSHXZipFile(shxFiles[0])
		if err != nil {
			return nil, err
		}
	default:
		return nil, errors.New("multiple .shx files") // FIXME
	}

	return &Shapefile{
		DBF: dbf,
		PRJ: prj,
		SHP: shp,
		SHX: shx,
	}, nil
}

func (s *Shapefile) Record(i int) (map[string]any, geom.T) {
	var fields map[string]any
	if s.DBF != nil {
		fields = s.DBF.Record(i)
	}
	var g geom.T
	if s.SHP != nil {
		g = s.SHP.Record(i)
	}
	return fields, g
}

func ReadSHxHeader(r io.Reader, fileLength int64) (*SHxHeader, error) {
	if fileLength < headerSize {
		return nil, errInvalidHeader
	}
	data := make([]byte, headerSize)
	if err := readFull(r, data); err != nil {
		return nil, err
	}
	return ParseSHxHeader(data, fileLength)
}

func ParseSHxHeader(data []byte, fileLength int64) (*SHxHeader, error) {
	if len(data) != headerSize {
		return nil, errInvalidFileLength
	}
	if headerFileCode := binary.BigEndian.Uint32(data[:4]); headerFileCode != fileCode {
		return nil, errInvalidFileCode
	}
	if headerFileLength := 2 * int64(binary.BigEndian.Uint32(data[24:28])); headerFileLength != fileLength {
		return nil, errInvalidFileLength
	}
	if headerVersion := binary.LittleEndian.Uint32(data[28:32]); headerVersion != version {
		return nil, errInvalidVersion
	}

	shapeType := ShapeType(binary.LittleEndian.Uint32(data[32:36]))
	if _, ok := knownShapeTypes[shapeType]; !ok {
		return nil, errInvalidShapeType
	}

	minX := math.Float64frombits(binary.LittleEndian.Uint64(data[36:44]))
	minY := math.Float64frombits(binary.LittleEndian.Uint64(data[44:52]))
	maxX := math.Float64frombits(binary.LittleEndian.Uint64(data[52:60]))
	maxY := math.Float64frombits(binary.LittleEndian.Uint64(data[60:68]))
	minZ := math.Float64frombits(binary.LittleEndian.Uint64(data[68:76]))
	maxZ := math.Float64frombits(binary.LittleEndian.Uint64(data[76:84]))
	minM := math.Float64frombits(binary.LittleEndian.Uint64(data[84:92]))
	maxM := math.Float64frombits(binary.LittleEndian.Uint64(data[92:100]))

	if NoData(minX) {
		minX = math.Inf(1)
	}
	if NoData(minY) {
		minY = math.Inf(1)
	}
	if NoData(maxX) {
		maxX = math.Inf(-1)
	}
	if NoData(maxY) {
		maxY = math.Inf(-1)
	}

	var bounds *geom.Bounds
	switch shapeType {
	case ShapeTypeNull:
	case ShapeTypePoint, ShapeTypeMultiPoint, ShapeTypePolyLine, ShapeTypePolygon:
		bounds = geom.NewBounds(geom.XY).Set(minX, minY, maxX, maxY)
	case ShapeTypePointM, ShapeTypeMultiPointM, ShapeTypePolyLineM, ShapeTypePolygonM:
		if NoData(minM) {
			minM = math.Inf(1)
		}
		if NoData(maxM) {
			maxM = math.Inf(-1)
		}
		bounds = geom.NewBounds(geom.XYM).Set(minX, minY, minM, maxX, maxY, maxM)
	case ShapeTypePointZ, ShapeTypeMultiPointZ, ShapeTypePolyLineZ, ShapeTypePolygonZ:
		if NoData(minM) {
			minM = math.Inf(1)
		}
		if NoData(maxM) {
			maxM = math.Inf(-1)
		}
		if NoData(minZ) {
			minZ = math.Inf(1)
		}
		if NoData(maxZ) {
			maxZ = math.Inf(-1)
		}
		bounds = geom.NewBounds(geom.XYZM).Set(minX, minY, minZ, minM, maxX, maxY, maxZ, maxM)
	}

	return &SHxHeader{
		ShapeType: shapeType,
		Bounds:    bounds,
	}, nil
}

func NoData(x float64) bool {
	return x <= -1e38
}

func readFull(r io.Reader, data []byte) error {
	for {
		switch n, err := r.Read(data); {
		case errors.Is(err, io.EOF) && n == len(data):
			return nil
		case err != nil:
			return err
		case n == 0:
			return io.EOF
		case n < len(data):
			data = data[n:]
		default:
			return nil
		}
	}
}
