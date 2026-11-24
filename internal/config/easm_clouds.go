package config

import (
	"strings"
	"unicode"
)

// Hosting categories of the portrait's cloud table. FOFA reports only a
// cloud_name string, so what a name means for the reader is maintained here
// rather than inferred from the reply.
const (
	CloudCategoryCDN      = "cdn"
	CloudCategoryWAF      = "waf"
	CloudCategoryCompute  = "cloud"
	CloudCategoryStorage  = "storage"
	CloudCategoryMail     = "mail"
	CloudCategoryPlatform = "platform"
	CloudCategoryOwn      = "own"
	CloudCategoryUnknown  = "unknown"
)

// EASMCloudCategories lists every category the producer may publish. The
// renderer words each one; an unlisted value would render as a blank cell.
var EASMCloudCategories = []string{
	CloudCategoryCDN,
	CloudCategoryWAF,
	CloudCategoryCompute,
	CloudCategoryStorage,
	CloudCategoryMail,
	CloudCategoryPlatform,
	CloudCategoryOwn,
	CloudCategoryUnknown,
}

// CloudLabel is one cloud_name's display name and hosting category.
type CloudLabel struct {
	Display  string `yaml:"display" json:"display"`
	Category string `yaml:"category" json:"category"`
}

// EASMCloudLabels translates FOFA's cloud_name values. Keys are lowercase: the
// source spells the same provider several ways (BaiShanCloud/BaishanCloud,
// Cloudfront/cloudfront) and the lowercase lookup merges them into one row.
//
// Categories describe what the label means for the reader, not what the vendor
// sells. A provider that offers both CDN and compute is filed under the form
// its cloud_name denotes: alikunlun is the CDN edge, aliyun the compute side,
// and tencent stays compute because FOFA reports mail separately as
// "Tencent Mail".
//
// This table is data. It is the one place a provider label may be written, and
// a name missing here is shown as the source spelled it with an unknown
// category rather than being guessed at.
var EASMCloudLabels = map[string]CloudLabel{
	// Edge delivery.
	"akamai":             {"阿卡迈", CloudCategoryCDN},
	"fastly":             {"Fastly", CloudCategoryCDN},
	"cloudflare":         {"Cloudflare", CloudCategoryCDN},
	"cloudfront":         {"CloudFront", CloudCategoryCDN},
	"alikunlun":          {"阿里昆仑", CloudCategoryCDN},
	"chinanetcenter":     {"网宿", CloudCategoryCDN},
	"baishancloud":       {"白山云", CloudCategoryCDN},
	"baishan cloud":      {"白山云", CloudCategoryCDN},
	"chinacache":         {"蓝汛", CloudCategoryCDN},
	"dnion":              {"帝联", CloudCategoryCDN},
	"upyun":              {"又拍云", CloudCategoryCDN},
	"qiniu":              {"七牛云", CloudCategoryCDN},
	"qiniuyun":           {"七牛云", CloudCategoryCDN},
	"taobao cdn":         {"淘宝 CDN", CloudCategoryCDN},
	"cdn77":              {"CDN77", CloudCategoryCDN},
	"cdnetworks":         {"CDNetworks", CloudCategoryCDN},
	"highwinds":          {"Highwinds", CloudCategoryCDN},
	"keycdn":             {"KeyCDN", CloudCategoryCDN},
	"leasewebcdn":        {"LeaseWeb CDN", CloudCategoryCDN},
	"limelight":          {"Limelight", CloudCategoryCDN},
	"stackpath":          {"StackPath", CloudCategoryCDN},
	"edgecast":           {"EdgeCast", CloudCategoryCDN},
	"mirrorimage":        {"Mirror Image", CloudCategoryCDN},
	"cachefly":           {"CacheFly", CloudCategoryCDN},
	"bunnycdn":           {"BunnyCDN", CloudCategoryCDN},
	"fastweb":            {"北京快网", CloudCategoryCDN},
	"cachemoment":        {"魔门云", CloudCategoryCDN},
	"maoyun":             {"猫云", CloudCategoryCDN},
	"webluker":           {"WebLuker", CloudCategoryCDN},
	"ucloud cdn":         {"UCloud CDN", CloudCategoryCDN},
	"zenlayer cdn":       {"Zenlayer CDN", CloudCategoryCDN},
	"tan14 cdn":          {"TAN14 CDN", CloudCategoryCDN},
	"verycdn":            {"云端网络", CloudCategoryCDN},
	"geeksforgeekscdn":   {"GeeksForGeeks CDN", CloudCategoryCDN},
	"airee":              {"Airee", CloudCategoryCDN},
	"myra":               {"Myra", CloudCategoryCDN},
	"china mobile cdn":   {"中国移动", CloudCategoryOwn},
	"turbo bytes":        {"TurboBytes", CloudCategoryCDN},
	"turbobytes":         {"TurboBytes", CloudCategoryCDN},
	"ananke":             {"Ananke", CloudCategoryCDN},
	"belugacdn":          {"BelugaCDN", CloudCategoryCDN},
	"lambdacdn":          {"LambdaCDN", CloudCategoryCDN},
	"instart cdn":        {"Instart CDN", CloudCategoryCDN},
	"internap cdn":       {"Internap CDN", CloudCategoryCDN},
	"cotendocdn":         {"Cotendo CDN", CloudCategoryCDN},
	"afxcdn.net":         {"afxcdn.net", CloudCategoryCDN},
	"cedexis gslb":       {"Cedexis 全局负载均衡", CloudCategoryCDN},
	"voxcdn":             {"VoxCDN", CloudCategoryCDN},
	"bluehatnetwork":     {"BlueHat Network", CloudCategoryCDN},
	"swiftcdn":           {"SwiftCDN", CloudCategoryCDN},
	"swiftserve":         {"SwiftServe", CloudCategoryCDN},
	"yottaa":             {"Yottaa", CloudCategoryCDN},
	"cubecdn":            {"cubeCDN", CloudCategoryCDN},
	"ngenix":             {"NGENIX", CloudCategoryCDN},
	"pagerain":           {"PageRain", CloudCategoryCDN},
	"quic cloud":         {"QUIC Cloud", CloudCategoryCDN},
	"flexbalancer":       {"FlexBalancer", CloudCategoryCDN},
	"azion":              {"Azion", CloudCategoryCDN},
	"mediacloud":         {"MediaCloud", CloudCategoryCDN},
	"reflected networks": {"Reflected Networks", CloudCategoryCDN},
	"simplecdn":          {"SimpleCDN", CloudCategoryCDN},
	"medianova":          {"Medianova", CloudCategoryCDN},
	"nyiftw":             {"NYIFTW", CloudCategoryCDN},
	"resrc.it":           {"ReSRC.it", CloudCategoryCDN},
	"revsoftware":        {"RevSoftware", CloudCategoryCDN},
	"caspowa":            {"Caspowa", CloudCategoryCDN},
	"section.io":         {"section.io", CloudCategoryCDN},
	"bisongrid":          {"BisonGrid", CloudCategoryCDN},
	"gocache":            {"GoCache", CloudCategoryCDN},
	"hiberniacdn":        {"HiberniaCDN", CloudCategoryCDN},
	"unicorncdn":         {"UnicornCDN", CloudCategoryCDN},
	"optimalcdn":         {"OptimalCDN", CloudCategoryCDN},
	"kinxcdn":            {"KINX CDN", CloudCategoryCDN},
	"hosting4cdn":        {"Hosting4CDN", CloudCategoryCDN},
	"skyparkcdn":         {"SkyparkCDN", CloudCategoryCDN},
	"lsy.cn":             {"蓝视云", CloudCategoryCDN},
	"cloudfloordns":      {"CloudfloorDNS", CloudCategoryCDN},
	"powercdn":           {"动力在线", CloudCategoryCDN},
	"txnetworks":         {"同兴万点", CloudCategoryCDN},
	"vangen cdn":         {"万根网络 CDN", CloudCategoryCDN},
	"chuangcache":        {"创世云", CloudCategoryCDN},
	"manmanyun":          {"蛮蛮云", CloudCategoryCDN},
	"ngaa":               {"新流云", CloudCategoryCDN},
	"xycloud":            {"星域云", CloudCategoryCDN},
	"linkingcloud":       {"领智云", CloudCategoryCDN},
	"ud cloud":           {"UD Cloud", CloudCategoryCDN},
	"kekaoyun":           {"可靠云", CloudCategoryCDN},
	"isurecloud":         {"云端智度", CloudCategoryCDN},
	"yfcloud":            {"云帆", CloudCategoryCDN},
	"allelink":           {"万物易联", CloudCategoryCDN},
	"cdnify":             {"CDNify", CloudCategoryCDN},
	"cdnsun":             {"CDNsun", CloudCategoryCDN},

	// Edge protection.
	"aliyundun":         {"阿里云盾", CloudCategoryWAF},
	"taobao waf":        {"淘宝 WAF", CloudCategoryWAF},
	"jiasule":           {"加速乐", CloudCategoryWAF},
	"qianxin":           {"奇安信", CloudCategoryWAF},
	"wangshen":          {"网神", CloudCategoryWAF},
	"360":               {"360", CloudCategoryWAF},
	"sangfor":           {"深信服", CloudCategoryWAF},
	"xuanwudun":         {"安恒玄武盾", CloudCategoryWAF},
	"30wangan":          {"30网安", CloudCategoryWAF},
	"westone":           {"卫士通", CloudCategoryWAF},
	"nelcisp":           {"等保云防", CloudCategoryWAF},
	"jvsit":             {"嘉韦思", CloudCategoryWAF},
	"anquanbang":        {"安全帮", CloudCategoryWAF},
	"stopddos":          {"极御云安全", CloudCategoryWAF},
	"yundun":            {"云盾", CloudCategoryWAF},
	"nsfocus":           {"绿盟", CloudCategoryWAF},
	"sucuri":            {"Sucuri", CloudCategoryWAF},
	"imperva incapsula": {"Imperva Incapsula", CloudCategoryWAF},
	"imunify360":        {"Imunify360", CloudCategoryWAF},
	"zenedge":           {"Zenedge", CloudCategoryWAF},
	"cloudfence":        {"蓝盾云防线", CloudCategoryWAF},
	"wangdi":            {"网堤", CloudCategoryWAF},
	"yundi":             {"云堤科技", CloudCategoryWAF},
	"zscaler":           {"Zscaler", CloudCategoryWAF},

	// Rented compute.
	"amazon aws":                      {"AWS", CloudCategoryCompute},
	"aws":                             {"AWS", CloudCategoryCompute},
	"amazon":                          {"AWS", CloudCategoryCompute},
	"aws elastic beanstalk":           {"AWS Elastic Beanstalk", CloudCategoryCompute},
	"aliyun":                          {"阿里云", CloudCategoryCompute},
	"alibaba":                         {"阿里巴巴", CloudCategoryCompute},
	"huaweicloud":                     {"华为云", CloudCategoryCompute},
	"tencent":                         {"腾讯云", CloudCategoryCompute},
	"jdcloud":                         {"京东云", CloudCategoryCompute},
	"qingcloud":                       {"青云", CloudCategoryCompute},
	"ucloud":                          {"UCloud", CloudCategoryCompute},
	"ksyun":                           {"金山云", CloudCategoryCompute},
	"meituanyun":                      {"美团云", CloudCategoryCompute},
	"didiyun":                         {"滴滴云", CloudCategoryCompute},
	"21vianet":                        {"世纪互联", CloudCategoryCompute},
	"dianxinyun":                      {"电信天翼云", CloudCategoryCompute},
	"china telecom cloud":             {"电信天翼云", CloudCategoryCompute},
	"chinatelecom":                    {"电信天翼云", CloudCategoryCompute},
	"e cloud":                         {"移动云", CloudCategoryCompute},
	"oracle":                          {"Oracle", CloudCategoryCompute},
	"baidu":                           {"百度", CloudCategoryCompute},
	"google":                          {"谷歌", CloudCategoryCompute},
	"microsoft azure":                 {"微软 Azure", CloudCategoryCompute},
	"microsoft azure traffic manager": {"微软 Azure", CloudCategoryCompute},
	"azure":                           {"微软 Azure", CloudCategoryCompute},
	"microsoft":                       {"微软 Azure", CloudCategoryCompute},
	"onapp":                           {"OnApp", CloudCategoryCompute},
	"clever-cloud":                    {"Clever-Cloud", CloudCategoryCompute},
	"rackspace":                       {"Rackspace", CloudCategoryCompute},
	"fastone":                         {"速石云", CloudCategoryCompute},
	"speedycloud":                     {"迅达云", CloudCategoryCompute},
	"west":                            {"西部数码", CloudCategoryCompute},
	"gosun":                           {"高升控股", CloudCategoryCompute},
	"sinacloud":                       {"新浪云", CloudCategoryCompute},

	// Object storage.
	"amazon s3":            {"Amazon S3", CloudCategoryStorage},
	"google cloud storage": {"谷歌云存储", CloudCategoryStorage},

	// Mail hosting and delivery.
	"alimail":                {"阿里企业邮箱", CloudCategoryMail},
	"tencent mail":           {"腾讯企业邮箱", CloudCategoryMail},
	"sendgrid email service": {"SendGrid", CloudCategoryMail},
	"godaddy email service":  {"GoDaddy 邮件", CloudCategoryMail},
	"263":                    {"263 企业邮箱", CloudCategoryMail},

	// Third-party hosting and SaaS.
	"github pages":       {"GitHub Pages", CloudCategoryPlatform},
	"gitlab pages":       {"GitLab Pages", CloudCategoryPlatform},
	"coding pages":       {"Coding Pages", CloudCategoryPlatform},
	"heroku saas":        {"Heroku SaaS", CloudCategoryPlatform},
	"netlify":            {"Netlify", CloudCategoryPlatform},
	"zeit now smart cdn": {"Vercel", CloudCategoryPlatform},
	"statuspage":         {"StatusPage", CloudCategoryPlatform},
	"zendesk":            {"Zendesk", CloudCategoryPlatform},
	"wordpress":          {"WordPress", CloudCategoryPlatform},
	"discord":            {"Discord", CloudCategoryPlatform},
	"emarsys":            {"Emarsys", CloudCategoryPlatform},
	"salesforce":         {"Salesforce", CloudCategoryPlatform},
	"office365":          {"Microsoft 365", CloudCategoryPlatform},
	"alipay":             {"支付宝", CloudCategoryPlatform},
	"meicheng":           {"美橙", CloudCategoryPlatform},
	"mmtrix":             {"性能魔方", CloudCategoryPlatform},

	// An organization's own network rather than a rented one.
	"xiaomi":              {"小米", CloudCategoryOwn},
	"bytedance":           {"字节跳动", CloudCategoryOwn},
	"wangyi":              {"网易", CloudCategoryOwn},
	"weipinhui":           {"唯品会", CloudCategoryOwn},
	"xiecheng":            {"携程", CloudCategoryOwn},
	"suning":              {"苏宁", CloudCategoryOwn},
	"eleme":               {"饿了么", CloudCategoryOwn},
	"sina":                {"新浪", CloudCategoryOwn},
	"jd":                  {"京东", CloudCategoryOwn},
	"twitter":             {"推特", CloudCategoryOwn},
	"facebook":            {"Facebook", CloudCategoryOwn},
	"yahoo":               {"雅虎", CloudCategoryOwn},
	"google web":          {"Google Web", CloudCategoryOwn},
	"level3":              {"Level 3", CloudCategoryOwn},
	"at&t":                {"AT&T", CloudCategoryOwn},
	"telefonica":          {"西班牙电信", CloudCategoryOwn},
	"telenor":             {"Telenor", CloudCategoryOwn},
	"orange business":     {"法国电信", CloudCategoryOwn},
	"tata communications": {"塔塔通信", CloudCategoryOwn},
	"aryaka":              {"Aryaka", CloudCategoryOwn},
}

// CloudLabelFor resolves one source name. The lookup is case-insensitive
// because the same provider arrives spelled several ways; an unmapped name is
// displayed as reported with no category instead of being guessed at.
func CloudLabelFor(name string) CloudLabel {
	label, ok := EASMCloudLabels[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return CloudLabel{Category: CloudCategoryUnknown}
	}
	return label
}

// CloudVendorNames lists the display names used to recognise a vendor label
// that leaked into another aggregation, e.g. "金山云-CDN" among product
// buckets. Short Latin names are left out: "360" and "AWS" also occur inside
// real product names, while a two-character CJK name such as "网宿" does not.
func CloudVendorNames() map[string]bool {
	names := make(map[string]bool, len(EASMCloudLabels))
	for _, label := range EASMCloudLabels {
		if unambiguousVendorName(label.Display) {
			names[strings.ToLower(label.Display)] = true
		}
	}
	return names
}

func unambiguousVendorName(display string) bool {
	ascii := true
	for _, r := range display {
		if r > unicode.MaxASCII {
			ascii = false
			break
		}
	}
	if ascii {
		return len(display) >= 4
	}
	return len([]rune(display)) >= 2
}
