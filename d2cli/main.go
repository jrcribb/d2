package d2cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/spf13/pflag"
	"go.uber.org/multierr"

	"oss.terrastruct.com/util-go/go2"
	"oss.terrastruct.com/util-go/xmain"

	"oss.terrastruct.com/d2/d2lib"
	"oss.terrastruct.com/d2/d2parser"
	"oss.terrastruct.com/d2/d2plugin"
	"oss.terrastruct.com/d2/d2renderers/d2animate"
	"oss.terrastruct.com/d2/d2renderers/d2fonts"
	"oss.terrastruct.com/d2/d2renderers/d2svg"
	"oss.terrastruct.com/d2/d2renderers/d2svg/appendix"
	"oss.terrastruct.com/d2/d2target"
	"oss.terrastruct.com/d2/d2themes"
	"oss.terrastruct.com/d2/d2themes/d2themescatalog"
	"oss.terrastruct.com/d2/lib/background"
	"oss.terrastruct.com/d2/lib/imgbundler"
	ctxlog "oss.terrastruct.com/d2/lib/log"
	pdflib "oss.terrastruct.com/d2/lib/pdf"
	"oss.terrastruct.com/d2/lib/png"
	"oss.terrastruct.com/d2/lib/pptx"
	"oss.terrastruct.com/d2/lib/textmeasure"
	"oss.terrastruct.com/d2/lib/version"

	"cdr.dev/slog"
	"cdr.dev/slog/sloggers/sloghuman"
)

func Run(ctx context.Context, ms *xmain.State) (err error) {
	// :(
	ctx = DiscardSlog(ctx)

	// These should be kept up-to-date with the d2 man page
	watchFlag, err := ms.Opts.Bool("D2_WATCH", "watch", "w", false, "watch for changes to input and live reload. Use $HOST and $PORT to specify the listening address.\n(default localhost:0, which is will open on a randomly available local port).")
	if err != nil {
		return err
	}
	hostFlag := ms.Opts.String("HOST", "host", "h", "localhost", "host listening address when used with watch")
	portFlag := ms.Opts.String("PORT", "port", "p", "0", "port listening address when used with watch")
	bundleFlag, err := ms.Opts.Bool("D2_BUNDLE", "bundle", "b", true, "when outputting SVG, bundle all assets and layers into the output file")
	if err != nil {
		return err
	}
	forceAppendixFlag, err := ms.Opts.Bool("D2_FORCE_APPENDIX", "force-appendix", "", false, "an appendix for tooltips and links is added to PNG exports since they are not interactive. --force-appendix adds an appendix to SVG exports as well")
	if err != nil {
		return err
	}
	debugFlag, err := ms.Opts.Bool("DEBUG", "debug", "d", false, "print debug logs.")
	if err != nil {
		return err
	}
	layoutFlag := ms.Opts.String("D2_LAYOUT", "layout", "l", "dagre", `the layout engine used`)
	themeFlag, err := ms.Opts.Int64("D2_THEME", "theme", "t", 0, "the diagram theme ID")
	if err != nil {
		return err
	}
	darkThemeFlag, err := ms.Opts.Int64("D2_DARK_THEME", "dark-theme", "", -1, "the theme to use when the viewer's browser is in dark mode. When left unset -theme is used for both light and dark mode. Be aware that explicit styles set in D2 code will still be applied and this may produce unexpected results. We plan on resolving this by making style maps in D2 light/dark mode specific. See https://github.com/terrastruct/d2/issues/831.")
	if err != nil {
		return err
	}
	padFlag, err := ms.Opts.Int64("D2_PAD", "pad", "", d2svg.DEFAULT_PADDING, "pixels padded around the rendered diagram")
	if err != nil {
		return err
	}
	animateIntervalFlag, err := ms.Opts.Int64("D2_ANIMATE_INTERVAL", "animate-interval", "", 0, "if given, multiple boards are packaged as 1 SVG which transitions through each board at the interval (in milliseconds). Can only be used with SVG exports.")
	if err != nil {
		return err
	}
	versionFlag, err := ms.Opts.Bool("", "version", "v", false, "get the version")
	if err != nil {
		return err
	}
	sketchFlag, err := ms.Opts.Bool("D2_SKETCH", "sketch", "s", false, "render the diagram to look like it was sketched by hand")
	if err != nil {
		return err
	}
	browserFlag := ms.Opts.String("BROWSER", "browser", "", "", "browser executable that watch opens. Setting to 0 opens no browser.")
	centerFlag, err := ms.Opts.Bool("D2_CENTER", "center", "c", false, "center the SVG in the containing viewbox, such as your browser screen")
	if err != nil {
		return err
	}

	fontRegularFlag := ms.Opts.String("D2_FONT_REGULAR", "font-regular", "", "", "path to .ttf file to use for the regular font. If none provided, Source Sans Pro Regular is used.")
	fontItalicFlag := ms.Opts.String("D2_FONT_ITALIC", "font-italic", "", "", "path to .ttf file to use for the italic font. If none provided, Source Sans Pro Regular-Italic is used.")
	fontBoldFlag := ms.Opts.String("D2_FONT_BOLD", "font-bold", "", "", "path to .ttf file to use for the bold font. If none provided, Source Sans Pro Bold is used.")

	ps, err := d2plugin.ListPlugins(ctx)
	if err != nil {
		return err
	}
	err = populateLayoutOpts(ctx, ms, ps)
	if err != nil {
		return err
	}

	err = ms.Opts.Flags.Parse(ms.Opts.Args)
	if !errors.Is(err, pflag.ErrHelp) && err != nil {
		return xmain.UsageErrorf("failed to parse flags: %v", err)
	}

	if errors.Is(err, pflag.ErrHelp) {
		help(ms)
		return nil
	}

	fontFamily, err := loadFonts(ms, *fontRegularFlag, *fontItalicFlag, *fontBoldFlag)
	if err != nil {
		return xmain.UsageErrorf("failed to load specified fonts: %v", err)
	}

	if len(ms.Opts.Flags.Args()) > 0 {
		switch ms.Opts.Flags.Arg(0) {
		case "init-playwright":
			return initPlaywright()
		case "layout":
			return layoutCmd(ctx, ms, ps)
		case "themes":
			themesCmd(ctx, ms)
			return nil
		case "fmt":
			return fmtCmd(ctx, ms)
		case "version":
			if len(ms.Opts.Flags.Args()) > 1 {
				return xmain.UsageErrorf("version subcommand accepts no arguments")
			}
			fmt.Println(version.Version)
			return nil
		}
	}

	if *debugFlag {
		ms.Env.Setenv("DEBUG", "1")
	}
	if *browserFlag != "" {
		ms.Env.Setenv("BROWSER", *browserFlag)
	}

	var inputPath string
	var outputPath string

	if len(ms.Opts.Flags.Args()) == 0 {
		if versionFlag != nil && *versionFlag {
			fmt.Println(version.Version)
			return nil
		}
		help(ms)
		return nil
	} else if len(ms.Opts.Flags.Args()) >= 3 {
		return xmain.UsageErrorf("too many arguments passed")
	}

	if len(ms.Opts.Flags.Args()) >= 1 {
		inputPath = ms.Opts.Flags.Arg(0)
	}
	if len(ms.Opts.Flags.Args()) >= 2 {
		outputPath = ms.Opts.Flags.Arg(1)
	} else {
		if inputPath == "-" {
			outputPath = "-"
		} else {
			outputPath = renameExt(inputPath, ".svg")
		}
	}
	if inputPath != "-" {
		inputPath = ms.AbsPath(inputPath)
		d, err := os.Stat(inputPath)
		if err == nil && d.IsDir() {
			inputPath = filepath.Join(inputPath, "index.d2")
		}
	}
	if outputPath != "-" {
		outputPath = ms.AbsPath(outputPath)
		if *animateIntervalFlag > 0 {
			// Not checking for extension == "svg", because users may want to write SVG data to a non-svg-extension file
			if filepath.Ext(outputPath) == ".png" || filepath.Ext(outputPath) == ".pdf" || filepath.Ext(outputPath) == ".pptx" {
				return xmain.UsageErrorf("-animate-interval can only be used when exporting to SVG.\nYou provided: %s", filepath.Ext(outputPath))
			}
		}
	}

	match := d2themescatalog.Find(*themeFlag)
	if match == (d2themes.Theme{}) {
		return xmain.UsageErrorf("-t[heme] could not be found. The available options are:\n%s\nYou provided: %d", d2themescatalog.CLIString(), *themeFlag)
	}
	ms.Log.Debug.Printf("using theme %s (ID: %d)", match.Name, *themeFlag)

	if *darkThemeFlag == -1 {
		darkThemeFlag = nil // TODO this is a temporary solution: https://github.com/terrastruct/util-go/issues/7
	}
	if darkThemeFlag != nil {
		match = d2themescatalog.Find(*darkThemeFlag)
		if match == (d2themes.Theme{}) {
			return xmain.UsageErrorf("--dark-theme could not be found. The available options are:\n%s\nYou provided: %d", d2themescatalog.CLIString(), *darkThemeFlag)
		}
		ms.Log.Debug.Printf("using dark theme %s (ID: %d)", match.Name, *darkThemeFlag)
	}

	plugin, err := d2plugin.FindPlugin(ctx, ps, *layoutFlag)
	if err != nil {
		if errors.Is(err, exec.ErrNotFound) {
			return layoutNotFound(ctx, ps, *layoutFlag)
		}
		return err
	}

	err = d2plugin.HydratePluginOpts(ctx, ms, plugin)
	if err != nil {
		return err
	}

	pinfo, err := plugin.Info(ctx)
	if err != nil {
		return err
	}
	plocation := pinfo.Type
	if pinfo.Type == "binary" {
		plocation = fmt.Sprintf("executable plugin at %s", humanPath(pinfo.Path))
	}
	ms.Log.Debug.Printf("using layout plugin %s (%s)", *layoutFlag, plocation)

	var pw png.Playwright
	if filepath.Ext(outputPath) == ".png" || filepath.Ext(outputPath) == ".pdf" || filepath.Ext(outputPath) == ".pptx" {
		if darkThemeFlag != nil {
			ms.Log.Warn.Printf("--dark-theme cannot be used while exporting to another format other than .svg")
			darkThemeFlag = nil
		}
		pw, err = png.InitPlaywright()
		if err != nil {
			return err
		}
		defer func() {
			cleanupErr := pw.Cleanup()
			if err == nil {
				err = cleanupErr
			}
		}()
	}

	renderOpts := d2svg.RenderOpts{
		Pad:         int(*padFlag),
		Sketch:      *sketchFlag,
		Center:      *centerFlag,
		ThemeID:     *themeFlag,
		DarkThemeID: darkThemeFlag,
	}

	if *watchFlag {
		if inputPath == "-" {
			return xmain.UsageErrorf("-w[atch] cannot be combined with reading input from stdin")
		}
		w, err := newWatcher(ctx, ms, watcherOpts{
			layoutPlugin:    plugin,
			renderOpts:      renderOpts,
			animateInterval: *animateIntervalFlag,
			host:            *hostFlag,
			port:            *portFlag,
			inputPath:       inputPath,
			outputPath:      outputPath,
			bundle:          *bundleFlag,
			forceAppendix:   *forceAppendixFlag,
			pw:              pw,
			fontFamily:      fontFamily,
		})
		if err != nil {
			return err
		}
		return w.run()
	}

	ctx, cancel := context.WithTimeout(ctx, time.Minute*2)
	defer cancel()

	_, written, err := compile(ctx, ms, plugin, renderOpts, fontFamily, *animateIntervalFlag, inputPath, outputPath, *bundleFlag, *forceAppendixFlag, pw.Page)
	if err != nil {
		if written {
			return fmt.Errorf("failed to fully compile (partial render written): %w", err)
		}
		return fmt.Errorf("failed to compile: %w", err)
	}
	return nil
}

func compile(ctx context.Context, ms *xmain.State, plugin d2plugin.Plugin, renderOpts d2svg.RenderOpts, fontFamily *d2fonts.FontFamily, animateInterval int64, inputPath, outputPath string, bundle, forceAppendix bool, page playwright.Page) (_ []byte, written bool, _ error) {
	start := time.Now()
	input, err := ms.ReadPath(inputPath)
	if err != nil {
		return nil, false, err
	}

	ruler, err := textmeasure.NewRuler()
	if err != nil {
		return nil, false, err
	}

	layout := plugin.Layout
	opts := &d2lib.CompileOptions{
		Layout:     layout,
		Ruler:      ruler,
		ThemeID:    renderOpts.ThemeID,
		FontFamily: fontFamily,
	}
	if renderOpts.Sketch {
		opts.FontFamily = go2.Pointer(d2fonts.HandDrawn)
	}

	cancel := background.Repeat(func() {
		ms.Log.Info.Printf("compiling & running layout algorithms...")
	}, time.Second*5)
	defer cancel()

	diagram, g, err := d2lib.Compile(ctx, string(input), opts)
	if err != nil {
		return nil, false, err
	}
	cancel()

	if animateInterval > 0 {
		masterID, err := diagram.HashID()
		if err != nil {
			return nil, false, err
		}
		renderOpts.MasterID = masterID
	}

	pluginInfo, err := plugin.Info(ctx)
	if err != nil {
		return nil, false, err
	}

	err = d2plugin.FeatureSupportCheck(pluginInfo, g)
	if err != nil {
		return nil, false, err
	}

	switch filepath.Ext(outputPath) {
	case ".pdf":
		pageMap := buildBoardIdToIndex(diagram, nil, nil)
		pdf, err := renderPDF(ctx, ms, plugin, renderOpts, outputPath, page, ruler, diagram, nil, nil, pageMap)
		if err != nil {
			return pdf, false, err
		}
		dur := time.Since(start)
		ms.Log.Success.Printf("successfully compiled %s to %s in %s", ms.HumanPath(inputPath), ms.HumanPath(outputPath), dur)
		return pdf, true, nil
	case ".pptx":
		var username string
		if user, err := user.Current(); err == nil {
			username = user.Username
		}
		description := "Presentation generated with D2 - https://d2lang.com/"
		rootName := getFileName(outputPath)
		// version must be only numbers to avoid issues with PowerPoint
		p := pptx.NewPresentation(rootName, description, rootName, username, version.OnlyNumbers())

		boardIdToIndex := buildBoardIdToIndex(diagram, nil, nil)
		svg, err := renderPPTX(ctx, ms, p, plugin, renderOpts, ruler, outputPath, page, diagram, nil, boardIdToIndex)
		if err != nil {
			return nil, false, err
		}
		err = p.SaveTo(outputPath)
		if err != nil {
			return nil, false, err
		}
		dur := time.Since(start)
		ms.Log.Success.Printf("successfully compiled %s to %s in %s", ms.HumanPath(inputPath), ms.HumanPath(outputPath), dur)
		return svg, true, nil
	default:
		compileDur := time.Since(start)
		if animateInterval <= 0 {
			// Rename all the "root.layers.x" to the paths that the boards get output to
			linkToOutput, err := resolveLinks("root", outputPath, diagram)
			if err != nil {
				return nil, false, err
			}
			relink(diagram, linkToOutput)
		}

		boards, err := render(ctx, ms, compileDur, plugin, renderOpts, inputPath, outputPath, bundle, forceAppendix, page, ruler, diagram)
		if err != nil {
			return nil, false, err
		}
		var out []byte
		if len(boards) > 0 {
			out = boards[0]
			if animateInterval > 0 {
				out, err = d2animate.Wrap(diagram, boards, renderOpts, int(animateInterval))
				if err != nil {
					return nil, false, err
				}
				err = os.MkdirAll(filepath.Dir(outputPath), 0755)
				if err != nil {
					return nil, false, err
				}
				err = ms.WritePath(outputPath, out)
				if err != nil {
					return nil, false, err
				}
				ms.Log.Success.Printf("successfully compiled %s to %s in %s", ms.HumanPath(inputPath), ms.HumanPath(outputPath), time.Since(start))
			}
		}
		return out, true, nil
	}
}

func resolveLinks(currDiagramPath, outputPath string, diagram *d2target.Diagram) (linkToOutput map[string]string, err error) {
	if diagram.Name != "" {
		ext := filepath.Ext(outputPath)
		outputPath = strings.TrimSuffix(outputPath, ext)
		outputPath = filepath.Join(outputPath, diagram.Name)
		outputPath += ext
	}

	boardOutputPath := outputPath
	if len(diagram.Layers) > 0 || len(diagram.Scenarios) > 0 || len(diagram.Steps) > 0 {
		ext := filepath.Ext(boardOutputPath)
		boardOutputPath = strings.TrimSuffix(boardOutputPath, ext)
		boardOutputPath = filepath.Join(boardOutputPath, "index")
		boardOutputPath += ext
	}

	layersOutputPath := outputPath
	if len(diagram.Scenarios) > 0 || len(diagram.Steps) > 0 {
		ext := filepath.Ext(layersOutputPath)
		layersOutputPath = strings.TrimSuffix(layersOutputPath, ext)
		layersOutputPath = filepath.Join(layersOutputPath, "layers")
		layersOutputPath += ext
	}
	scenariosOutputPath := outputPath
	if len(diagram.Layers) > 0 || len(diagram.Steps) > 0 {
		ext := filepath.Ext(scenariosOutputPath)
		scenariosOutputPath = strings.TrimSuffix(scenariosOutputPath, ext)
		scenariosOutputPath = filepath.Join(scenariosOutputPath, "scenarios")
		scenariosOutputPath += ext
	}
	stepsOutputPath := outputPath
	if len(diagram.Layers) > 0 || len(diagram.Scenarios) > 0 {
		ext := filepath.Ext(stepsOutputPath)
		stepsOutputPath = strings.TrimSuffix(stepsOutputPath, ext)
		stepsOutputPath = filepath.Join(stepsOutputPath, "steps")
		stepsOutputPath += ext
	}

	linkToOutput = map[string]string{currDiagramPath: boardOutputPath}

	for _, dl := range diagram.Layers {
		m, err := resolveLinks(strings.Join([]string{currDiagramPath, "layers", dl.Name}, "."), layersOutputPath, dl)
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			linkToOutput[k] = v
		}
	}
	for _, dl := range diagram.Scenarios {
		m, err := resolveLinks(strings.Join([]string{currDiagramPath, "scenarios", dl.Name}, "."), scenariosOutputPath, dl)
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			linkToOutput[k] = v
		}
	}
	for _, dl := range diagram.Steps {
		m, err := resolveLinks(strings.Join([]string{currDiagramPath, "steps", dl.Name}, "."), stepsOutputPath, dl)
		if err != nil {
			return nil, err
		}
		for k, v := range m {
			linkToOutput[k] = v
		}
	}

	return linkToOutput, nil
}

func relink(d *d2target.Diagram, linkToOutput map[string]string) {
	for i, shape := range d.Shapes {
		if shape.Link != "" {
			for k, v := range linkToOutput {
				if shape.Link == k {
					d.Shapes[i].Link = v
					break
				}
			}
		}
	}
	for _, board := range d.Layers {
		relink(board, linkToOutput)
	}
	for _, board := range d.Scenarios {
		relink(board, linkToOutput)
	}
	for _, board := range d.Steps {
		relink(board, linkToOutput)
	}
}

func render(ctx context.Context, ms *xmain.State, compileDur time.Duration, plugin d2plugin.Plugin, opts d2svg.RenderOpts, inputPath, outputPath string, bundle, forceAppendix bool, page playwright.Page, ruler *textmeasure.Ruler, diagram *d2target.Diagram) ([][]byte, error) {
	if diagram.Name != "" {
		ext := filepath.Ext(outputPath)
		outputPath = strings.TrimSuffix(outputPath, ext)
		outputPath = filepath.Join(outputPath, diagram.Name)
		outputPath += ext
	}

	boardOutputPath := outputPath
	if len(diagram.Layers) > 0 || len(diagram.Scenarios) > 0 || len(diagram.Steps) > 0 {
		if outputPath == "-" {
			// TODO it can if composed into one
			return nil, fmt.Errorf("multiboard output cannot be written to stdout")
		}
		// Boards with subboards must be self-contained folders.
		ext := filepath.Ext(boardOutputPath)
		boardOutputPath = strings.TrimSuffix(boardOutputPath, ext)
		os.RemoveAll(boardOutputPath)
		boardOutputPath = filepath.Join(boardOutputPath, "index")
		boardOutputPath += ext
	}

	layersOutputPath := outputPath
	if len(diagram.Scenarios) > 0 || len(diagram.Steps) > 0 {
		ext := filepath.Ext(layersOutputPath)
		layersOutputPath = strings.TrimSuffix(layersOutputPath, ext)
		layersOutputPath = filepath.Join(layersOutputPath, "layers")
		layersOutputPath += ext
	}
	scenariosOutputPath := outputPath
	if len(diagram.Layers) > 0 || len(diagram.Steps) > 0 {
		ext := filepath.Ext(scenariosOutputPath)
		scenariosOutputPath = strings.TrimSuffix(scenariosOutputPath, ext)
		scenariosOutputPath = filepath.Join(scenariosOutputPath, "scenarios")
		scenariosOutputPath += ext
	}
	stepsOutputPath := outputPath
	if len(diagram.Layers) > 0 || len(diagram.Scenarios) > 0 {
		ext := filepath.Ext(stepsOutputPath)
		stepsOutputPath = strings.TrimSuffix(stepsOutputPath, ext)
		stepsOutputPath = filepath.Join(stepsOutputPath, "steps")
		stepsOutputPath += ext
	}

	var boards [][]byte
	for _, dl := range diagram.Layers {
		childrenBoards, err := render(ctx, ms, compileDur, plugin, opts, inputPath, layersOutputPath, bundle, forceAppendix, page, ruler, dl)
		if err != nil {
			return nil, err
		}
		boards = append(boards, childrenBoards...)
	}
	for _, dl := range diagram.Scenarios {
		childrenBoards, err := render(ctx, ms, compileDur, plugin, opts, inputPath, scenariosOutputPath, bundle, forceAppendix, page, ruler, dl)
		if err != nil {
			return nil, err
		}
		boards = append(boards, childrenBoards...)
	}
	for _, dl := range diagram.Steps {
		childrenBoards, err := render(ctx, ms, compileDur, plugin, opts, inputPath, stepsOutputPath, bundle, forceAppendix, page, ruler, dl)
		if err != nil {
			return nil, err
		}
		boards = append(boards, childrenBoards...)
	}

	if !diagram.IsFolderOnly {
		start := time.Now()
		out, err := _render(ctx, ms, plugin, opts, boardOutputPath, bundle, forceAppendix, page, ruler, diagram)
		if err != nil {
			return boards, err
		}
		dur := compileDur + time.Since(start)
		if opts.MasterID == "" {
			ms.Log.Success.Printf("successfully compiled %s to %s in %s", ms.HumanPath(inputPath), ms.HumanPath(boardOutputPath), dur)
		}
		boards = append([][]byte{out}, boards...)
		return boards, nil
	}

	return nil, nil
}

func _render(ctx context.Context, ms *xmain.State, plugin d2plugin.Plugin, opts d2svg.RenderOpts, outputPath string, bundle, forceAppendix bool, page playwright.Page, ruler *textmeasure.Ruler, diagram *d2target.Diagram) ([]byte, error) {
	toPNG := filepath.Ext(outputPath) == ".png"
	svg, err := d2svg.Render(diagram, &d2svg.RenderOpts{
		Pad:           opts.Pad,
		Sketch:        opts.Sketch,
		Center:        opts.Center,
		ThemeID:       opts.ThemeID,
		DarkThemeID:   opts.DarkThemeID,
		MasterID:      opts.MasterID,
		SetDimensions: toPNG,
	})
	if err != nil {
		return nil, err
	}

	svg, err = plugin.PostProcess(ctx, svg)
	if err != nil {
		return svg, err
	}

	svg, bundleErr := imgbundler.BundleLocal(ctx, ms, svg)
	if bundle {
		var bundleErr2 error
		svg, bundleErr2 = imgbundler.BundleRemote(ctx, ms, svg)
		bundleErr = multierr.Combine(bundleErr, bundleErr2)
	}
	if forceAppendix && !toPNG {
		svg = appendix.Append(diagram, ruler, svg)
	}

	out := svg
	if toPNG {
		svg := appendix.Append(diagram, ruler, svg)

		if !bundle {
			var bundleErr2 error
			svg, bundleErr2 = imgbundler.BundleRemote(ctx, ms, svg)
			bundleErr = multierr.Combine(bundleErr, bundleErr2)
		}

		out, err = png.ConvertSVG(ms, page, svg)
		if err != nil {
			return svg, err
		}
		out, err = png.AddExif(out)
		if err != nil {
			return svg, err
		}
	} else {
		if len(out) > 0 && out[len(out)-1] != '\n' {
			out = append(out, '\n')
		}
	}

	if opts.MasterID == "" {
		err = os.MkdirAll(filepath.Dir(outputPath), 0755)
		if err != nil {
			return svg, err
		}
		err = ms.WritePath(outputPath, out)
		if err != nil {
			return svg, err
		}
	}
	if bundleErr != nil {
		return svg, bundleErr
	}
	return svg, nil
}

func renderPDF(ctx context.Context, ms *xmain.State, plugin d2plugin.Plugin, opts d2svg.RenderOpts, outputPath string, page playwright.Page, ruler *textmeasure.Ruler, diagram *d2target.Diagram, pdf *pdflib.GoFPDF, boardPath []string, pageMap map[string]int) (svg []byte, err error) {
	var isRoot bool
	if pdf == nil {
		pdf = pdflib.Init()
		isRoot = true
	}

	var currBoardPath []string
	// Root board doesn't have a name, so we use the output filename
	if diagram.Name == "" {
		currBoardPath = append(boardPath, getFileName(outputPath))
	} else {
		currBoardPath = append(boardPath, diagram.Name)
	}

	if !diagram.IsFolderOnly {
		rootFill := diagram.Root.Fill
		// gofpdf will print the png img with a slight filter
		// make the bg fill within the png transparent so that the pdf bg fill is the only bg color present
		diagram.Root.Fill = "transparent"

		svg, err = d2svg.Render(diagram, &d2svg.RenderOpts{
			Pad:           opts.Pad,
			Sketch:        opts.Sketch,
			Center:        opts.Center,
			SetDimensions: true,
		})
		if err != nil {
			return nil, err
		}

		svg, err = plugin.PostProcess(ctx, svg)
		if err != nil {
			return svg, err
		}

		svg, bundleErr := imgbundler.BundleLocal(ctx, ms, svg)
		svg, bundleErr2 := imgbundler.BundleRemote(ctx, ms, svg)
		bundleErr = multierr.Combine(bundleErr, bundleErr2)
		if bundleErr != nil {
			return svg, bundleErr
		}
		svg = appendix.Append(diagram, ruler, svg)

		pngImg, err := png.ConvertSVG(ms, page, svg)
		if err != nil {
			return svg, err
		}

		viewboxSlice := appendix.FindViewboxSlice(svg)
		viewboxX, err := strconv.ParseFloat(viewboxSlice[0], 64)
		if err != nil {
			return svg, err
		}
		viewboxY, err := strconv.ParseFloat(viewboxSlice[1], 64)
		if err != nil {
			return svg, err
		}
		err = pdf.AddPDFPage(pngImg, currBoardPath, opts.ThemeID, rootFill, diagram.Shapes, int64(opts.Pad), viewboxX, viewboxY, pageMap)
		if err != nil {
			return svg, err
		}
	}

	for _, dl := range diagram.Layers {
		_, err := renderPDF(ctx, ms, plugin, opts, "", page, ruler, dl, pdf, currBoardPath, pageMap)
		if err != nil {
			return nil, err
		}
	}
	for _, dl := range diagram.Scenarios {
		_, err := renderPDF(ctx, ms, plugin, opts, "", page, ruler, dl, pdf, currBoardPath, pageMap)
		if err != nil {
			return nil, err
		}
	}
	for _, dl := range diagram.Steps {
		_, err := renderPDF(ctx, ms, plugin, opts, "", page, ruler, dl, pdf, currBoardPath, pageMap)
		if err != nil {
			return nil, err
		}
	}

	if isRoot {
		err := pdf.Export(outputPath)
		if err != nil {
			return nil, err
		}
	}

	return svg, nil
}

func renderPPTX(ctx context.Context, ms *xmain.State, presentation *pptx.Presentation, plugin d2plugin.Plugin, opts d2svg.RenderOpts, ruler *textmeasure.Ruler, outputPath string, page playwright.Page, diagram *d2target.Diagram, boardPath []string, boardIdToIndex map[string]int) ([]byte, error) {
	var currBoardPath []string
	// Root board doesn't have a name, so we use the output filename
	if diagram.Name == "" {
		currBoardPath = append(boardPath, getFileName(outputPath))
	} else {
		currBoardPath = append(boardPath, diagram.Name)
	}

	var svg []byte
	if !diagram.IsFolderOnly {
		// gofpdf will print the png img with a slight filter
		// make the bg fill within the png transparent so that the pdf bg fill is the only bg color present
		diagram.Root.Fill = "transparent"

		var err error
		svg, err = d2svg.Render(diagram, &d2svg.RenderOpts{
			Pad:           opts.Pad,
			Sketch:        opts.Sketch,
			Center:        opts.Center,
			SetDimensions: true,
		})
		if err != nil {
			return nil, err
		}

		svg, err = plugin.PostProcess(ctx, svg)
		if err != nil {
			return nil, err
		}

		svg, bundleErr := imgbundler.BundleLocal(ctx, ms, svg)
		svg, bundleErr2 := imgbundler.BundleRemote(ctx, ms, svg)
		bundleErr = multierr.Combine(bundleErr, bundleErr2)
		if bundleErr != nil {
			return nil, bundleErr
		}

		svg = appendix.Append(diagram, ruler, svg)

		// png.ConvertSVG scales the image by 2x
		pngScale := 2.
		pngImg, err := png.ConvertSVG(ms, page, svg)
		if err != nil {
			return nil, err
		}

		slide, err := presentation.AddSlide(pngImg, currBoardPath)
		if err != nil {
			return nil, err
		}

		viewboxSlice := appendix.FindViewboxSlice(svg)
		viewboxX, err := strconv.ParseFloat(viewboxSlice[0], 64)
		if err != nil {
			return nil, err
		}
		viewboxY, err := strconv.ParseFloat(viewboxSlice[1], 64)
		if err != nil {
			return nil, err
		}

		// Draw links
		for _, shape := range diagram.Shapes {
			if shape.Link == "" {
				continue
			}

			linkX := pngScale * (float64(shape.Pos.X) - viewboxX - float64(shape.StrokeWidth))
			linkY := pngScale * (float64(shape.Pos.Y) - viewboxY - float64(shape.StrokeWidth))
			linkWidth := pngScale * (float64(shape.Width) + float64(shape.StrokeWidth*2))
			linkHeight := pngScale * (float64(shape.Height) + float64(shape.StrokeWidth*2))
			link := &pptx.Link{
				Left:    int(linkX),
				Top:     int(linkY),
				Width:   int(linkWidth),
				Height:  int(linkHeight),
				Tooltip: shape.Link,
			}
			slide.AddLink(link)
			key, err := d2parser.ParseKey(shape.Link)
			if err != nil || key.Path[0].Unbox().ScalarString() != "root" {
				// External link
				link.ExternalUrl = shape.Link
			} else if pageNum, ok := boardIdToIndex[shape.Link]; ok {
				// Internal link
				link.SlideIndex = pageNum + 1
			}
		}
	}

	for _, dl := range diagram.Layers {
		_, err := renderPPTX(ctx, ms, presentation, plugin, opts, ruler, "", page, dl, currBoardPath, boardIdToIndex)
		if err != nil {
			return nil, err
		}
	}
	for _, dl := range diagram.Scenarios {
		_, err := renderPPTX(ctx, ms, presentation, plugin, opts, ruler, "", page, dl, currBoardPath, boardIdToIndex)
		if err != nil {
			return nil, err
		}
	}
	for _, dl := range diagram.Steps {
		_, err := renderPPTX(ctx, ms, presentation, plugin, opts, ruler, "", page, dl, currBoardPath, boardIdToIndex)
		if err != nil {
			return nil, err
		}
	}

	return svg, nil
}

// newExt must include leading .
func renameExt(fp string, newExt string) string {
	ext := filepath.Ext(fp)
	if ext == "" {
		return fp + newExt
	} else {
		return strings.TrimSuffix(fp, ext) + newExt
	}
}

func getFileName(path string) string {
	ext := filepath.Ext(path)
	return strings.TrimSuffix(filepath.Base(path), ext)
}

// TODO: remove after removing slog
func DiscardSlog(ctx context.Context) context.Context {
	return ctxlog.With(ctx, slog.Make(sloghuman.Sink(io.Discard)))
}

func populateLayoutOpts(ctx context.Context, ms *xmain.State, ps []d2plugin.Plugin) error {
	pluginFlags, err := d2plugin.ListPluginFlags(ctx, ps)
	if err != nil {
		return err
	}

	for _, f := range pluginFlags {
		f.AddToOpts(ms.Opts)
		// Don't pollute the main d2 flagset with these. It'll be a lot
		ms.Opts.Flags.MarkHidden(f.Name)
	}

	return nil
}

func initPlaywright() error {
	pw, err := png.InitPlaywright()
	if err != nil {
		return err
	}
	return pw.Cleanup()
}

func loadFont(ms *xmain.State, path string) ([]byte, error) {
	if filepath.Ext(path) != ".ttf" {
		return nil, fmt.Errorf("expected .ttf file but %s has extension %s", path, filepath.Ext(path))
	}
	ttf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read font at %s: %v", path, err)
	}
	ms.Log.Info.Printf("font %s loaded", filepath.Base(path))
	return ttf, nil
}

func loadFonts(ms *xmain.State, pathToRegular, pathToItalic, pathToBold string) (*d2fonts.FontFamily, error) {
	if pathToRegular == "" && pathToItalic == "" && pathToBold == "" {
		return nil, nil
	}

	var regularTTF []byte
	var italicTTF []byte
	var boldTTF []byte

	var err error
	if pathToRegular != "" {
		regularTTF, err = loadFont(ms, pathToRegular)
		if err != nil {
			return nil, err
		}
	}
	if pathToItalic != "" {
		italicTTF, err = loadFont(ms, pathToItalic)
		if err != nil {
			return nil, err
		}
	}
	if pathToBold != "" {
		boldTTF, err = loadFont(ms, pathToBold)
		if err != nil {
			return nil, err
		}
	}

	return d2fonts.AddFontFamily("custom", regularTTF, italicTTF, boldTTF)
}

// buildBoardIdToIndex returns a map from board path to page int
// To map correctly, it must follow the same traversal of PDF building
func buildBoardIdToIndex(diagram *d2target.Diagram, dictionary map[string]int, path []string) map[string]int {
	newPath := append(path, diagram.Name)
	if dictionary == nil {
		dictionary = map[string]int{}
		newPath[0] = "root"
	}

	key := strings.Join(newPath, ".")
	dictionary[key] = len(dictionary)

	for _, dl := range diagram.Layers {
		buildBoardIdToIndex(dl, dictionary, append(newPath, "layers"))
	}
	for _, dl := range diagram.Scenarios {
		buildBoardIdToIndex(dl, dictionary, append(newPath, "scenarios"))
	}
	for _, dl := range diagram.Steps {
		buildBoardIdToIndex(dl, dictionary, append(newPath, "steps"))
	}

	return dictionary
}
