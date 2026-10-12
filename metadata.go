package dovi

// Header preserves the evidence used to infer the coarse Dolby Vision profile.
// Profile 8 does not establish a base-layer compatibility subprofile.
type Header struct {
	Format                                          uint16
	Profile, Level                                  uint8
	SequenceInfo, ChromaFilter                      bool
	CoefficientType                                 uint8
	CoefficientDenominator                          uint8
	NormalizedIDC                                   uint8
	FullRange                                       bool
	BLBitDepth, ELBitDepth, VDRBitDepth             uint8
	ExtendedMapping                                 uint8
	SpatialFilter, ELSpatialFilter, DisableResidual bool
	Reserved                                        uint8
	DMPresent, ReuseMapping                         bool
	PreviousID                                      uint64
}

type L1 struct{ MinPQ, MaxPQ, AvgPQ uint16 }
type L5 struct{ Left, Right, Top, Bottom uint16 }

// L6 units are those encoded on the wire: maximum luminance/CLL/FALL in
// cd/m² and minimum mastering luminance in 0.0001 cd/m².
type L6 struct{ MaxMastering, MinMastering, MaxCLL, MaxFALL uint16 }

// Block retains validated extension fields in their documented wire order.
// Length distinguishes optional absent fields from encoded zero values.
// L2's final signed 13-bit field is sign extended into Values.
type Block struct {
	Level  uint8
	Length uint16
	Values []int64
}

// BaseDM is absent for compressed DM, whose omitted values are unknown.
type BaseDM struct {
	YCCToRGB                                      [9]int16
	YCCOffsets                                    [3]uint32
	RGBToLMS                                      [9]int16
	EOTF, EOTFParam0, EOTFParam1                  uint16
	EOTFParam2                                    uint32
	BitDepth, ColorSpace, ChromaFormat, FullRange uint8
	SourceMinPQ, SourceMaxPQ, SourceDiagonal      uint16
}

type DM struct {
	Compressed                          bool
	AffectedID, CurrentID, SceneRefresh uint64
	Base                                *BaseDM
	CM29, CM40                          bool
	Blocks                              []Block
	L1                                  *L1
	L5                                  *L5
	L6                                  *L6
}

// RPU owns all its metadata. Unknown enhancement-layer classification is "";
// reuse does not inherit metadata or mapping state from an earlier record.
type RPU struct {
	Header           Header
	Profile          uint8
	EnhancementLayer string
	MappingID        uint64
	DM               *DM
	Remaining        []byte
	CRC              uint32
}
