package pdf

// QR-штамп: размеры и отступы в миллиметрах. Источник правды — здесь.
const (
	QRStampSizeMM       = 51.0 // 85% от исходных 60мм
	QRStampMarginRight  = 10.0 // отступ от правого края страницы, мм
	QRStampMarginBottom = 10.0 // отступ от нижнего края страницы, мм
	QRStampGapMM        = 4.0  // зазор между соседними штампами по горизонтали, мм
	QRStampBGPadding    = 2.0  // белая подложка вокруг QR, мм (см. TODO в stamp.go)
)

// MMToPt — конверсия миллиметров в PDF user units (1pt = 1/72 дюйма).
const MMToPt = 2.83464566929
