package output

// Densities of a run's output as a timeline shows it, the least first. Each client draws the same table
// (docs/design/runs/output.md); tools/protogen writes it into the Web UI's proto.js.
const (
	DensityBrief    = "brief"
	DensityStandard = "standard"
	DensityDetailed = "detailed"
)

var Densities = []string{DensityBrief, DensityStandard, DensityDetailed}

const DefaultDensity = DensityStandard

// How a step shows at a density.
const (
	ShowHide = "hide" // no row, not in the turn's summary
	ShowSum  = "sum"  // counted in the turn's one-line summary; a failed one has its own row
	ShowRow  = "row"  // its own row, closed
	ShowOpen = "open" // its own row, open
	ShowWarn = "warn" // a row only for a warning
	ShowHook = "hook" // a row only for a hook's mark
	ShowNote = "note" // a row only when it carries an error, usage or cost
)

// DensityShow is how each kind of step shows, at the densities in Densities' order. A step's kind is an event's kind,
// a tool call's family, group for a run of reads and searches, you for what people said, output for a result on its
// own; failures, the command that still runs and an unanswered question open at any density.
var DensityShow = map[string][3]string{
	"you":       {ShowRow, ShowRow, ShowRow},
	"say":       {ShowRow, ShowRow, ShowRow},
	"ask":       {ShowOpen, ShowOpen, ShowOpen},
	"error":     {ShowRow, ShowRow, ShowRow},
	"interrupt": {ShowRow, ShowRow, ShowRow},
	"gap":       {ShowRow, ShowRow, ShowRow},
	"think":     {ShowHide, ShowRow, ShowOpen},
	"mark":      {ShowHide, ShowHook, ShowHook},
	"sys":       {ShowHide, ShowWarn, ShowRow},
	"raw":       {ShowHide, ShowWarn, ShowRow},
	"result":    {ShowNote, ShowNote, ShowRow},
	"plan":      {ShowSum, ShowRow, ShowOpen},
	"shell":     {ShowSum, ShowRow, ShowOpen},
	"group":     {ShowSum, ShowRow, ShowOpen},
	"edit":      {ShowSum, ShowRow, ShowOpen},
	"web":       {ShowSum, ShowRow, ShowOpen},
	"agent":     {ShowSum, ShowRow, ShowOpen},
	"mcp":       {ShowSum, ShowRow, ShowOpen},
	"other":     {ShowSum, ShowRow, ShowOpen},
	"output":    {ShowSum, ShowRow, ShowOpen},
}

// Line counts the densities cut at.
const (
	BriefLines  = 3   // the lines of a brief shown before brief and standard fold it
	SayFold     = 30  // an agent's text longer than this folds in standard
	DiffCut     = 200 // a diff longer than this is cut in detailed
	FailTail    = 8   // the last lines of a failed output open on its own
	DetailEnds  = 10  // the head and tail of an output in detailed
	RunningTail = 3   // the last lines of a command that still runs
)
