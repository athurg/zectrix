package main

import (
	"flag"
	"fmt"
	"image/png"
	"os"

	"github.com/athurg/zectrix/textimg"
)

const sampleBody = "道可道，非常道；名可名，非常名。无名天地之始，" +
	"有名万物之母。故常无欲，以观其妙；常有欲，以观其徼。" +
	"此两者同出而异名，同谓之玄。玄之又玄，众妙之门。" +
	"上善若水，水善利万物而不争，处众人之所恶，故几于道。" +
	"居善地，心善渊，与善仁，言善信，事善能，动善时。" +
	"知人者智，自知者明。胜人者有力，自胜者强。知足者富。" +
	"强行者有志，不失其所者久，死而不亡者寿。知足不辱，" +
	"知止不殆，可以长久。为学日益，为道日损。损之又损，" +
	"以至于无为，无为而无不为。取天下常以无事，" +
	"大方无隅，大器晚成，大音希声，大象无形，道隐无名。" +
	"大直若屈，大巧若拙，大辩若讷。躁胜寒，静胜热。" +
	"清静为天下正。祸兮福之所倚，福兮祸之所伏。" +
	"治大国若烹小鲜。以道莅天下，其鬼不神。非其鬼不神，" +
	"其神不伤人，圣人亦不伤人。夫两不相伤，故德交归焉。" +
	"天下皆知美之为美，斯恶已；皆知善之为善，斯不善已。" +
	"故有无相生，难易相成，长短相形，高下相倾。"

func main() {
	output := flag.String("output", "textimg-preview.png", "output PNG file")
	fontPath := flag.String("font", "", "font file (default: embedded Zfull-GB)")
	title := flag.String("title", "通知", "title text")
	body := flag.String("body", sampleBody, "body text")
	footer := flag.String("footer", "电量 73% · Wi-Fi -61 dBm（良好） · 17:40 更新", "footer text")
	titleSize := flag.Int("title-size", 16, "title font size in pixels")
	bodySize := flag.Int("body-size", 14, "body font size in pixels")
	flag.Parse()

	font, err := textimg.LoadFont(*fontPath)
	if err != nil {
		fatal(err)
	}
	img, err := textimg.RenderWithOptions(font, *title, *body, *footer, textimg.Options{
		TitleSize: *titleSize,
		BodySize:  *bodySize,
	})
	if err != nil {
		fatal(err)
	}
	file, err := os.Create(*output)
	if err != nil {
		fatal(err)
	}
	if err := png.Encode(file, img); err != nil {
		file.Close()
		fatal(err)
	}
	if err := file.Close(); err != nil {
		fatal(err)
	}
	fmt.Println(*output)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "render-preview:", err)
	os.Exit(1)
}
