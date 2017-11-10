# 图片代理Improxy
## Improxy API说明
* CDN Host：
	* CDN_HOST: http://xxxx.cloudfront.net

* 图片代理使用格式:
  * 格式:
	  * {CDN_HOST}/tools/im/{options}/{s3_key}
  	  * {CDN_HOST}/tools/im/{options}/{s3_key}/ts{image_version}
  * 例如：
	* http://xxx.cloudfront.net/tools/im/200/xx/xx/xxx/profile.jpg/ts1490782085
	* http://xxx.cloudfront.net/tools/im/0/xx/xx/xxx/xx/cover_image_best.jpg
 * 图片格式:
	 * 支持jpeg/jpg, png, webp, gif
	 * 如果原始图片是gif, 则返回图片一定是gif
	 * 默认情况下，返回图片格式为jpeg或webp。取决于浏览器或手机app的支持情况
	 * 如果原始图片为png, 有透明效果，则需要通过options来强制指定返回图片的格式, 例如: fpng表示format为png
	 * 非图片格式的文件直接返回404，也就是不能通过improxy来读取图片以外的资源，例如: 文本

## URL结构

### options(缩放选项)
为了统一URL, 参数按照如下顺序出现:
* size,fit,r90,fv,q90,fpng
* 这些参数都是可选的


#### Size
* 一般情况下格式为: `{width}x{height}`, 其中宽度和高度为整数
* 如果其中一个为0，或不写，则按照另一边的尺寸来等比例缩放
	* 例如:
		* 500x0, 500x 表示图片的最大宽度为500
		* 0x500, x500 表示图片的最大高度为500
* 如果没有x, 例如: 500则表示目标图片的宽度和高度都为500
	* 500 <==> 500x500
* 如果给定`{width}x{height}`， 其中width, height都大于0，

#### Crop Mode(裁剪模式)
* 任何情况下，imageproxy都不会破坏图片原有的比例
* 如果同时制定了长和宽，则采用Fill模式，裁剪掉多余的部分
 * 如果`没有指定fit`模式，且原始的图片的长或宽小于指定的width, height, 那么最终的width, height会被等比例缩小，直到原始的图片能够fill完全
  * width x scale <= ImageWidth
  * height x scale <= ImageHeight
  * 最终生效的图片的宽高为: (width x scale, height x scale), 其中scale是满足上面条件的最大的值
* If only one of the width or height values is specified, the image will be
   resized to fit the specified dimension, scaling the other dimension as
   needed to maintain the aspect ratio.
* If the `fit` option is specified together with a width and height value, the
image will be resized to fit within a containing box of the specified size.  As
always, the original aspect ratio will be preserved. Specifying the `fit`
option with only one of either width or height does the same thing as if `fit`
had not been specified.


#### Rotate
通过`r{degrees}`让图片在resize之后，逆时针旋转`90`, `180` 或 `270`.

#### Flip
`fv` 垂直翻转，`fh` 水平翻转.  Images are flipped **after** being resized and rotated.

#### Quality
`q{percentage}` 指定JPEG文件的质量.  默认值是 `95`.

### Examples ###

The following live examples demonstrate setting different options on [this
source image][small-things], which measures 1024 by 678 pixels.

[small-things]: https://willnorris.com/2013/12/small-things.jpg
