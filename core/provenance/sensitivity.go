package provenance

type Sensitivity string

const (
	SensitivityPublic       Sensitivity = "public"
	SensitivityInternal     Sensitivity = "internal"
	SensitivityConfidential Sensitivity = "confidential"
	SensitivityRestricted   Sensitivity = "restricted"
	SensitivitySecret       Sensitivity = "secret"
)

var sensitivityOrder = map[Sensitivity]int{
	SensitivityPublic:       0,
	SensitivityInternal:     1,
	SensitivityConfidential: 2,
	SensitivityRestricted:   3,
	SensitivitySecret:       4,
}

func (s Sensitivity) Valid() bool {
	_, ok := sensitivityOrder[s]
	return ok
}

func (s Sensitivity) RequiresRedaction() bool {
	level, ok := sensitivityOrder[s]
	return ok && level >= sensitivityOrder[SensitivityConfidential]
}
