package shape

import (
	"math"

	"oss.terrastruct.com/d2/lib/geo"
	"oss.terrastruct.com/d2/lib/svg"
	"oss.terrastruct.com/util-go/go2"
)

// Optimal value for circular arc approximation with cubic bezier curves
const kCircleApprox = 0.5522847498307936 // 4*(math.Sqrt(2)-1)/3

// Constants to match frontend implementation
const (
	C4_PERSON_AR_LIMIT   = 1.5
	HEAD_RADIUS_FACTOR   = 0.22
	BODY_TOP_FACTOR      = 0.8
	CORNER_RADIUS_FACTOR = 0.175
)

type shapeC4Person struct {
	*baseShape
}

func NewC4Person(box *geo.Box) Shape {
	shape := shapeC4Person{
		baseShape: &baseShape{
			Type: C4_PERSON_TYPE,
			Box:  box,
		},
	}
	shape.FullShape = go2.Pointer(Shape(shape))
	return shape
}

func (s shapeC4Person) GetInnerBox() *geo.Box {
	width := s.Box.Width
	height := s.Box.Height

	headRadius := math.Round(width * HEAD_RADIUS_FACTOR)
	headCenterY := headRadius
	bodyTop := math.Round(headCenterY + headRadius*BODY_TOP_FACTOR)

	// Horizontal padding = 5% of width
	horizontalPadding := math.Round(width * 0.05)
	// Vertical padding = 3% of height
	verticalPadding := math.Round(height * 0.03)

	tl := s.Box.TopLeft.Copy()
	tl.X += horizontalPadding
	tl.Y += bodyTop + verticalPadding

	innerWidth := math.Round(width - (horizontalPadding * 2))
	innerHeight := math.Round(height - bodyTop - (verticalPadding * 2))

	return geo.NewBox(tl, innerWidth, innerHeight)
}

func bodyPath(box *geo.Box) *svg.SvgPathContext {
	width := box.Width
	height := box.Height

	pc := svg.NewSVGPathContext(box.TopLeft, 1, 1)

	headRadius := math.Round(width * HEAD_RADIUS_FACTOR)
	headCenterY := headRadius
	bodyTop := math.Round(headCenterY + headRadius*BODY_TOP_FACTOR)
	bodyWidth := width
	bodyHeight := math.Round(height - bodyTop)
	bodyLeft := 0

	cornerRadius := math.Round(math.Min(width*CORNER_RADIUS_FACTOR, bodyHeight*0.25))

	pc.StartAt(pc.Absolute(float64(bodyLeft), bodyTop+cornerRadius))

	pc.C(true, 0, -kCircleApprox*cornerRadius, kCircleApprox*cornerRadius, -cornerRadius, cornerRadius, -cornerRadius)
	pc.H(true, bodyWidth-2*cornerRadius)
	pc.C(true, kCircleApprox*cornerRadius, 0, cornerRadius, kCircleApprox*cornerRadius, cornerRadius, cornerRadius)
	pc.V(true, bodyHeight-2*cornerRadius)
	pc.C(true, 0, kCircleApprox*cornerRadius, -kCircleApprox*cornerRadius, cornerRadius, -cornerRadius, cornerRadius)
	pc.H(true, -(bodyWidth - 2*cornerRadius))
	pc.C(true, -kCircleApprox*cornerRadius, 0, -cornerRadius, -kCircleApprox*cornerRadius, -cornerRadius, -cornerRadius)
	pc.Z()

	return pc
}

func headPath(box *geo.Box) *svg.SvgPathContext {
	width := box.Width

	pc := svg.NewSVGPathContext(box.TopLeft, 1, 1)

	headRadius := math.Round(width * HEAD_RADIUS_FACTOR)
	headCenterX := math.Round(width / 2)
	headCenterY := headRadius

	pc.StartAt(pc.Absolute(headCenterX, headCenterY-headRadius))

	pc.C(false,
		headCenterX+headRadius*kCircleApprox, headCenterY-headRadius,
		headCenterX+headRadius, headCenterY-headRadius*kCircleApprox,
		headCenterX+headRadius, headCenterY)

	pc.C(false,
		headCenterX+headRadius, headCenterY+headRadius*kCircleApprox,
		headCenterX+headRadius*kCircleApprox, headCenterY+headRadius,
		headCenterX, headCenterY+headRadius)

	pc.C(false,
		headCenterX-headRadius*kCircleApprox, headCenterY+headRadius,
		headCenterX-headRadius, headCenterY+headRadius*kCircleApprox,
		headCenterX-headRadius, headCenterY)

	pc.C(false,
		headCenterX-headRadius, headCenterY-headRadius*kCircleApprox,
		headCenterX-headRadius*kCircleApprox, headCenterY-headRadius,
		headCenterX, headCenterY-headRadius)

	return pc
}

func (s shapeC4Person) Perimeter() []geo.Intersectable {
	width := s.Box.Width

	bodyPerimeter := bodyPath(s.Box).Path

	headRadius := math.Round(width * HEAD_RADIUS_FACTOR)
	headCenterX := s.Box.TopLeft.X + math.Round(width/2)
	headCenterY := s.Box.TopLeft.Y + headRadius
	headCenter := geo.NewPoint(headCenterX, headCenterY)

	headEllipse := geo.NewEllipse(headCenter, headRadius, headRadius)

	return append(bodyPerimeter, headEllipse)
}

func (s shapeC4Person) GetSVGPathData() []string {
	return []string{
		bodyPath(s.Box).PathData(),
		headPath(s.Box).PathData(),
	}
}

func (s shapeC4Person) GetDimensionsToFit(width, height, paddingX, paddingY float64) (float64, float64) {
	contentWidth := width + paddingX
	contentHeight := height + paddingY

	// Account for 10% total horizontal padding (5% on each side)
	totalWidth := math.Round(contentWidth / 0.9)
	headRadius := math.Round(totalWidth * HEAD_RADIUS_FACTOR)

	// Use positioning matching frontend
	headCenterY := headRadius
	bodyTop := math.Round(headCenterY + headRadius*BODY_TOP_FACTOR)

	// Include vertical padding
	verticalPadding := math.Round(totalWidth * 0.06) // 3% top + 3% bottom
	totalHeight := math.Round(contentHeight + bodyTop + verticalPadding)

	// Calculate minimum height
	minHeight := math.Round(totalWidth * 0.95)
	if totalHeight < minHeight {
		totalHeight = minHeight
	}

	totalWidth, totalHeight = LimitAR(totalWidth, totalHeight, C4_PERSON_AR_LIMIT)
	return math.Ceil(totalWidth), math.Ceil(totalHeight)
}

func (s shapeC4Person) GetDefaultPadding() (paddingX, paddingY float64) {
	return 10, defaultPadding
}
