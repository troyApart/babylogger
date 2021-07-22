package main

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
	"github.com/aws/aws-sdk-go/service/dynamodb/expression"
	"github.com/b-b3rn4rd/aws-lambda-runtime-golang/pkg/runtime"
	"github.com/kelseyhightower/envconfig"
	log "github.com/sirupsen/logrus"
)

const (
	LeftSide          = "left"
	RightSide         = "right"
	BottleSide        = "bottle"
	LatestFeedRequest = "next"
	NewFeedRequest    = "feed"
	UpdateFeedRequest = "update"
	NewDiaperRequest  = "diaper"
	ListDiapers       = "list diapers"
	ListFeeds         = "list feeds"
	UserID            = int64(1)
)

type Config struct {
	FeedingTableName string
	FeedingInterval  time.Duration
	DiaperTableName  string
}

func main() {
	c := Config{}
	err := envconfig.Process("BABYLOGGER", &c)
	if err != nil {
		log.Fatal(err.Error())
	}
	log.WithFields(log.Fields{
		"feeding_table":    c.FeedingTableName,
		"feeding_interval": c.FeedingInterval,
		"diaper_table":     c.DiaperTableName}).
		Info("config loaded")

	s := session.Must(session.NewSession())
	db := dynamodb.New(s)

	blh := BabyLogger{
		config:   &c,
		dynamodb: db,
	}

	runtime.Start(blh.Router)
}

type dynamodber interface {
	PutItem(*dynamodb.PutItemInput) (*dynamodb.PutItemOutput, error)
	UpdateItem(*dynamodb.UpdateItemInput) (*dynamodb.UpdateItemOutput, error)
	Query(*dynamodb.QueryInput) (*dynamodb.QueryOutput, error)
}

type BabyLogger struct {
	config   *Config
	dynamodb dynamodber
}

func (b *BabyLogger) Router(req events.APIGatewayProxyRequest) (events.APIGatewayProxyResponse, error) {
	log.WithFields(log.Fields{
		"headers":      req.Headers,
		"body":         req.Body,
		"query_string": req.QueryStringParameters,
		"method":       req.HTTPMethod}).Info("incoming request")

	if req.QueryStringParameters == nil {
		return clientError(http.StatusBadRequest)
	}
	xmlResp := &Response{}

	switch req.HTTPMethod {
	case "GET":
		urlUnescape, err := url.QueryUnescape(req.QueryStringParameters["Body"])
		if err != nil {
			break
		}
		message := strings.ToLower(urlUnescape)
		if strings.HasPrefix(message, LatestFeedRequest) {
			return b.NextFeed(message)
		} else if strings.HasPrefix(message, NewFeedRequest) {
			return b.NewFeed(message)
		} else if strings.HasPrefix(message, UpdateFeedRequest) {
			return b.UpdateFeed(message)
		} else if strings.HasPrefix(message, NewDiaperRequest) {
			return b.NewDiaper(message)
		} else if strings.HasPrefix(message, ListFeeds) {
			return b.ListFeeds(message)
		} else if strings.HasPrefix(message, ListDiapers) {
			return b.ListDiapers(message)
		}
	default:
		xmlResp.Message = "Only GET method allowed"
		resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
		if err != nil {
			return serverError(err)
		}
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusMethodNotAllowed,
			Body:       string(resp),
			Headers: map[string]string{
				"content-type": "text/xml",
			},
		}, nil
	}
	xmlResp.Message = fmt.Sprintf("List of available commands:\n%s\n%s\n%s\n%s\n%s\n%s", LatestFeedRequest, NewFeedRequest, UpdateFeedRequest, NewDiaperRequest, ListFeeds, ListDiapers)

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}
	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

func serverError(err error) (events.APIGatewayProxyResponse, error) {
	log.WithField("error", err.Error()).Error("server error")

	var xmlResp Response
	xmlResp.Message = http.StatusText(http.StatusInternalServerError)

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

func clientError(status int) (events.APIGatewayProxyResponse, error) {
	log.Error("client error")

	var xmlResp Response
	xmlResp.Message = http.StatusText(status)

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

type Response struct {
	XMLName xml.Name `xml:"Response"`
	Message string   `xml:"Message"`
}

type FeedingRecord struct {
	Timestamp int64  `json:"timestamp"`
	Side      string `json:"side"`
}

type FullFeedingRecord struct {
	Timestamp int64   `json:"timestamp"`
	Side      string  `json:"side"`
	Left      int64   `json:"left,omitempty"`
	Right     int64   `json:"right,omitempty"`
	Bottle    float64 `json:"bottle,omitempty"`
}

type FullDiaperRecord struct {
	Timestamp int64  `json:"timestamp"`
	Wet       bool   `json:"wet"`
	Soiled    bool   `json:"soiled"`
	Checked   string `json:"checked,omitempty"`
}

func getLast(tableName string, db dynamodber, skipBottle bool) (*FeedingRecord, error) {
	var lastTimestamp int64
	var keyCond *expression.KeyConditionBuilder
	for i := 1; i < 5; i++ {
		builder := expression.NewBuilder()
		key := expression.Key("userid").Equal(expression.Value(UserID))
		if keyCond != nil {
			key = expression.KeyAnd(key, *keyCond)
		}
		proj := expression.NamesList(expression.Name("timestamp"), expression.Name("side"))
		expr, err := builder.WithKeyCondition(key).WithProjection(proj).Build()
		if err != nil {
			return nil, err
		}

		qi := &dynamodb.QueryInput{
			TableName:                 aws.String(tableName),
			Limit:                     aws.Int64(1),
			ScanIndexForward:          aws.Bool(false),
			ExpressionAttributeNames:  expr.Names(),
			ExpressionAttributeValues: expr.Values(),
			ProjectionExpression:      expr.Projection(),
			KeyConditionExpression:    expr.KeyCondition(),
		}

		o, err := db.Query(qi)
		if err != nil {
			return nil, err
		}
		log.WithField("output", o.String()).Info("dynamodb query succeeded")

		if len(o.Items) != 1 {
			return nil, err
		}

		var fr FeedingRecord
		err = dynamodbattribute.UnmarshalMap(o.Items[0], &fr)
		if err != nil {
			return nil, err
		}
		if skipBottle && fr.Side == BottleSide {
			if lastTimestamp == 0 {
				lastTimestamp = fr.Timestamp
			}
			newKeyCond := expression.Key("timestamp").LessThan(expression.Value(fr.Timestamp))
			keyCond = &newKeyCond
			continue
		}
		if skipBottle && lastTimestamp != 0 {
			fr.Timestamp = lastTimestamp
		}
		return &fr, nil
	}
	return nil, fmt.Errorf("no data found")
}

// NextFeed - Gets the latest feeding and responds with expected next feeding and which side
func (b *BabyLogger) NextFeed(message string) (events.APIGatewayProxyResponse, error) {
	fr, err := getLast(b.config.FeedingTableName, b.dynamodb, true)
	if err != nil {
		return serverError(err)
	}

	var timeInterval time.Duration
	intervalRE := regexp.MustCompile(`.*(?P<interval>\d+h).*`)
	intervalMatch := intervalRE.FindStringSubmatch(message)
	intervalIndex := intervalRE.SubexpIndex("interval")
	if len(intervalMatch) > 0 {
		timeInterval, _ = time.ParseDuration(intervalMatch[intervalIndex])
	}
	if timeInterval.Hours() == 0 {
		timeInterval = b.config.FeedingInterval
	}

	countRE := regexp.MustCompile(`.*(?P<count>\d+)(\s+|$)`)
	countMatch := countRE.FindStringSubmatch(message)
	countIndex := countRE.SubexpIndex("count")
	var count int64
	if len(countMatch) > 0 {
		countString := countMatch[countIndex]
		count, err = strconv.ParseInt(countString, 0, 64)
		if err != nil {
			count = 1
		}
	}

	previousTime := time.Unix(fr.Timestamp, 0)
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return serverError(err)
	}

	xmlResp := &Response{}
	nextTime := previousTime.Add(timeInterval).In(loc)
	var nextSide string
	if fr.Side == LeftSide {
		nextSide = RightSide
	} else if fr.Side == RightSide {
		nextSide = LeftSide
	} else {
		nextSide = "unknown"
	}
	if count > 1 {
		xmlResp.Message = "The next feedings are:"
		for i := int64(0); i < count; i++ {
			xmlResp.Message = fmt.Sprintf("%s\n%s: %s", xmlResp.Message, nextSide, nextTime.Format("Jan 2 03:04PM"))
			nextTime = nextTime.Add(timeInterval).In(loc)
			if nextSide == LeftSide {
				nextSide = RightSide
			} else if nextSide == RightSide {
				nextSide = LeftSide
			} else {
				nextSide = "unknown"
			}
		}
	} else {
		xmlResp.Message = fmt.Sprintf("The next feeding is on your %s side on %s", nextSide, nextTime.Format("Jan 2 03:04PM"))
	}

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

func getAllForDay(tableName string, db dynamodber, dt *Datetime, proj expression.ProjectionBuilder, output interface{}) error {
	date := time.Unix(dt.timestamp, 0).In(dt.loc).Format("2006-01-02")
	dayStart, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s 00:00", date), dt.loc)
	if err != nil {
		return err
	}
	dayEnd, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s 23:59", date), dt.loc)
	if err != nil {
		return err
	}
	dayStartTimestamp := dayStart.Unix()
	dayEndTimestamp := dayEnd.Unix()

	key := expression.KeyEqual(expression.Key("userid"), expression.Value(UserID))
	key = expression.KeyAnd(key, expression.Key("timestamp").Between(expression.Value(dayStartTimestamp), expression.Value(dayEndTimestamp)))
	expr, err := expression.NewBuilder().WithKeyCondition(key).WithProjection(proj).Build()
	if err != nil {
		return err
	}

	qi := &dynamodb.QueryInput{
		TableName:                 aws.String(tableName),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		ProjectionExpression:      expr.Projection(),
		KeyConditionExpression:    expr.KeyCondition(),
	}

	o, err := db.Query(qi)
	if err != nil {
		return err
	}
	log.WithField("output", o.String()).Info("dynamodb query succeeded")

	err = dynamodbattribute.UnmarshalListOfMaps(o.Items, &output)
	if err != nil {
		return err
	}

	return nil
}

func (b *BabyLogger) ListFeeds(message string) (events.APIGatewayProxyResponse, error) {
	dt, err := getTimestamp(message)
	if err != nil {
		return serverError(err)
	}

	proj := expression.NamesList(
		expression.Name("timestamp"),
		expression.Name("side"),
		expression.Name("left"),
		expression.Name("right"),
		expression.Name("bottle"))
	ffr := make([]FullFeedingRecord, 0)
	err = getAllForDay(b.config.FeedingTableName, b.dynamodb, dt, proj, &ffr)
	if err != nil {
		return serverError(err)
	}

	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("Feedings on %s", time.Unix(dt.timestamp, 0).In(dt.loc).Format("2006-01-02"))
	var left, right, count int64
	var bottle float64
	for _, record := range ffr {
		timeInLoc := time.Unix(record.Timestamp, 0).In(dt.loc).Format("15:04")
		xmlResp.Message = fmt.Sprintf("%s\n%s -", xmlResp.Message, timeInLoc)
		count++
		if record.Side == BottleSide {
			xmlResp.Message = fmt.Sprintf("%s %s %.2foz", xmlResp.Message, record.Side, record.Bottle)
			bottle += record.Bottle
		} else {
			xmlResp.Message = fmt.Sprintf("%s %s L: %dmin R: %dmin", xmlResp.Message, record.Side, record.Left, record.Right)
			left += record.Left
			right += record.Right
		}
	}
	xmlResp.Message = fmt.Sprintf("%s\nTotal: %d, Left: %dmin, Right: %dmin, Bottle: %.2foz", xmlResp.Message, count, left, right, bottle)

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

func (b *BabyLogger) ListDiapers(message string) (events.APIGatewayProxyResponse, error) {
	dt, err := getTimestamp(message)
	if err != nil {
		return serverError(err)
	}

	proj := expression.NamesList(expression.Name("timestamp"), expression.Name("wet"), expression.Name("soiled"))
	fdr := make([]FullDiaperRecord, 0)
	err = getAllForDay(b.config.DiaperTableName, b.dynamodb, dt, proj, &fdr)
	if err != nil {
		return serverError(err)
	}

	xmlResp := &Response{}
	xmlResp.Message = fmt.Sprintf("Diapers on %s", time.Unix(dt.timestamp, 0).In(dt.loc).Format("2006-01-02"))
	var total, wet, soiled int
	for _, record := range fdr {
		timeInLoc := time.Unix(record.Timestamp, 0).In(dt.loc).Format("15:04")
		xmlResp.Message = fmt.Sprintf("%s\n%s - ", xmlResp.Message, timeInLoc)
		if record.Wet {
			xmlResp.Message = fmt.Sprintf("%s %s", xmlResp.Message, "Wet")
			wet++
		}
		if record.Soiled {
			xmlResp.Message = fmt.Sprintf("%s %s", xmlResp.Message, "Soiled")
			soiled++
		}
		total++
	}
	xmlResp.Message = fmt.Sprintf("%s\nTotal: %d, Wet: %d, Soiled: %d", xmlResp.Message, total, wet, soiled)

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

func (b *BabyLogger) NewFeed(message string) (events.APIGatewayProxyResponse, error) {
	re := regexp.MustCompile(`^feed (?P<side>[A-Za-z]+).*`)
	match := re.FindStringSubmatch(message)
	index := re.SubexpIndex("side")
	side := match[index]
	if side != LeftSide && side != RightSide && side != BottleSide {
		return clientError(http.StatusBadRequest)
	}

	dt, err := getDatetime(message)
	if err != nil {
		return serverError(err)
	}

	leftRE := regexp.MustCompile(`.*left (?P<left>\d+){0,1}.*`)
	leftMatch := leftRE.FindStringSubmatch(message)
	leftIndex := leftRE.SubexpIndex("left")
	var left string
	if len(leftMatch) > 0 {
		left = leftMatch[leftIndex]
	}

	rightRE := regexp.MustCompile(`.*right (?P<right>\d+){0,1}.*`)
	rightMatch := rightRE.FindStringSubmatch(message)
	rightIndex := rightRE.SubexpIndex("right")
	var right string
	if len(rightMatch) > 0 {
		right = rightMatch[rightIndex]
	}

	bottleRE := regexp.MustCompile(`.*bottle (?P<bottle>\d*\.{0,1}\d{0,2}){0,1}.*`)
	bottleMatch := bottleRE.FindStringSubmatch(message)
	bottleIndex := bottleRE.SubexpIndex("bottle")
	var bottle string
	if len(bottleMatch) > 0 {
		bottle = bottleMatch[bottleIndex]
	}

	i := &dynamodb.PutItemInput{
		TableName: aws.String(b.config.FeedingTableName),
		Item: map[string]*dynamodb.AttributeValue{
			"userid": {
				N: aws.String(strconv.Itoa(int(UserID))),
			},
			"timestamp": {
				N: aws.String(strconv.Itoa(int(dt.datetime.Unix()))),
			},
			"side": {
				S: aws.String(side),
			},
		},
	}
	if left != "" {
		i.Item["left"] = &dynamodb.AttributeValue{N: aws.String(left)}
	}
	if right != "" {
		i.Item["right"] = &dynamodb.AttributeValue{N: aws.String(right)}
	}
	if bottle != "" {
		i.Item["bottle"] = &dynamodb.AttributeValue{N: aws.String(bottle)}
	}
	o, err := b.dynamodb.PutItem(i)
	if err != nil {
		return serverError(err)
	}
	log.WithField("output", o).Info("dynamodb put succeeded")

	var sideString string
	if side == BottleSide {
		sideString = fmt.Sprintf("using %s", side)
	} else {
		sideString = fmt.Sprintf("starting on %s side", side)
	}
	xmlResp := &Response{}
	if dt.twentyFourHourTime {
		xmlResp.Message = fmt.Sprintf("New feeding recorded on %s %s", dt.datetime.In(dt.loc).Format("Jan 2 15:04"), sideString)
	} else {
		xmlResp.Message = fmt.Sprintf("New feeding recorded on %s %s", dt.datetime.In(dt.loc).Format("Jan 2 03:04PM"), sideString)
	}

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusCreated,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

func (b *BabyLogger) UpdateFeed(message string) (events.APIGatewayProxyResponse, error) {
	re := regexp.MustCompile(`^update last.*`)
	match := re.FindStringSubmatch(message)
	var dt *Datetime
	if len(match) != 0 {
		fr, err := getLast(b.config.FeedingTableName, b.dynamodb, false)
		if err != nil {
			return serverError(err)
		}

		dt = &Datetime{}
		dt.datetime = time.Unix(fr.Timestamp, 0)
		loc, err := time.LoadLocation("America/Los_Angeles")
		if err != nil {
			return serverError(err)
		}
		dt.loc = loc
	} else {
		var err error
		dt, err = getDatetime(message)
		if err != nil {
			return serverError(err)
		}
	}

	cond := expression.Name("timestamp").Equal(expression.Value(dt.datetime.Unix()))
	builder := expression.NewBuilder().WithCondition(cond)
	var update expression.UpdateBuilder

	leftRE := regexp.MustCompile(`.*left (?P<set>(set|add|sub)){0,1}\s*(?P<left>\d+){0,1}.*`)
	leftMatch := leftRE.FindStringSubmatch(message)
	leftSetIndex := leftRE.SubexpIndex("set")
	leftIndex := leftRE.SubexpIndex("left")
	var left string
	if len(leftMatch) > 0 {
		left = leftMatch[leftIndex]
		leftValue, err := strconv.ParseInt(left, 0, 64)
		if err != nil {
			return serverError(err)
		}
		switch leftMatch[leftSetIndex] {
		case "add":
			update = update.Add(expression.Name("left"), expression.Value(leftValue))
		case "sub":
			update = update.Add(expression.Name("left"), expression.Value(-leftValue))
		default:
			update = update.Set(expression.Name("left"), expression.Value(leftValue))
		}

	}

	rightRE := regexp.MustCompile(`.*right (?P<set>(set|add|sub)){0,1}\s*(?P<right>\d+){0,1}.*`)
	rightMatch := rightRE.FindStringSubmatch(message)
	rightSetIndex := rightRE.SubexpIndex("set")
	rightIndex := rightRE.SubexpIndex("right")
	var right string
	if len(rightMatch) > 0 {
		right = rightMatch[rightIndex]
		rightValue, err := strconv.ParseInt(right, 0, 64)
		if err != nil {
			return serverError(err)
		}
		switch rightMatch[rightSetIndex] {
		case "add":
			update = update.Add(expression.Name("right"), expression.Value(rightValue))
		case "sub":
			update = update.Add(expression.Name("right"), expression.Value(-rightValue))
		default:
			update = update.Set(expression.Name("right"), expression.Value(rightValue))
		}
	}

	bottleRE := regexp.MustCompile(`.*bottle (?P<set>(set|add|sub)){0,1}\s*(?P<bottle>\d*\.{0,1}\d{0,2}){0,1}.*`)
	bottleMatch := bottleRE.FindStringSubmatch(message)
	bottleSetIndex := bottleRE.SubexpIndex("set")
	bottleIndex := bottleRE.SubexpIndex("bottle")
	var bottle string
	if len(bottleMatch) > 0 {
		bottle = bottleMatch[bottleIndex]
		bottleValue, err := strconv.ParseFloat(bottle, 64)
		if err != nil {
			return serverError(err)
		}
		switch bottleMatch[bottleSetIndex] {
		case "add":
			update = update.Add(expression.Name("bottle"), expression.Value(bottleValue))
		case "sub":
			update = update.Add(expression.Name("bottle"), expression.Value(-bottleValue))
		default:
			update = update.Set(expression.Name("bottle"), expression.Value(bottleValue))
		}
	}

	if left == "" && right == "" && bottle == "" {
		return clientError(http.StatusBadRequest)
	}

	builder.WithUpdate(update)
	expr, err := builder.Build()
	if err != nil {
		return serverError(err)
	}

	i := &dynamodb.UpdateItemInput{
		TableName: aws.String(b.config.FeedingTableName),
		Key: map[string]*dynamodb.AttributeValue{
			"userid": {
				N: aws.String(strconv.Itoa(int(UserID))),
			},
			"timestamp": {
				N: aws.String(strconv.Itoa(int(dt.datetime.Unix()))),
			},
		},
		ExpressionAttributeValues: expr.Values(),
		ExpressionAttributeNames:  expr.Names(),
		ConditionExpression:       expr.Condition(),
		ReturnValues:              aws.String("UPDATED_NEW"),
		UpdateExpression:          expr.Update(),
	}

	o, err := b.dynamodb.UpdateItem(i)
	if err != nil {
		return serverError(err)
	}
	log.WithField("output", o).Info("dynamodb put succeeded")

	xmlResp := &Response{}
	if dt.twentyFourHourTime {
		xmlResp.Message = fmt.Sprintf("Updated feeding recorded on %s", dt.datetime.In(dt.loc).Format("Jan 2 15:04"))
	} else {
		xmlResp.Message = fmt.Sprintf("Updated feeding recorded on %s", dt.datetime.In(dt.loc).Format("Jan 2 03:04PM"))
	}

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusOK,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

func (b *BabyLogger) NewDiaper(message string) (events.APIGatewayProxyResponse, error) {
	dt, err := getDatetime(message)
	if err != nil {
		return serverError(err)
	}

	var wet, soiled bool
	if strings.Contains(message, "wet") {
		wet = true
	}
	if strings.Contains(message, "soiled") {
		soiled = true
	}

	checkedRE := regexp.MustCompile(`.*checked (?P<checked>[A-Za-z]+-{0,1}[A-Za-z]*){0,1}.*`)
	checkedMatch := checkedRE.FindStringSubmatch(message)
	checkedIndex := checkedRE.SubexpIndex("checked")
	var checked string
	if len(checkedMatch) > 0 {
		checked = checkedMatch[checkedIndex]
	}

	i := &dynamodb.PutItemInput{
		TableName: aws.String(b.config.DiaperTableName),
		Item: map[string]*dynamodb.AttributeValue{
			"userid": {
				N: aws.String(strconv.Itoa(int(UserID))),
			},
			"timestamp": {
				N: aws.String(strconv.Itoa(int(dt.datetime.Unix()))),
			},
			"wet": {
				BOOL: aws.Bool(wet),
			},
			"soiled": {
				BOOL: aws.Bool(soiled),
			},
		},
	}
	if checked != "" {
		i.Item["checked"] = &dynamodb.AttributeValue{S: aws.String(checked)}
	}
	o, err := b.dynamodb.PutItem(i)
	if err != nil {
		return serverError(err)
	}
	log.WithField("output", o).Info("dynamodb put succeeded")

	xmlResp := &Response{}
	if dt.twentyFourHourTime {
		xmlResp.Message = fmt.Sprintf("New diaper recorded on %s", dt.datetime.In(dt.loc).Format("Jan 2 15:04"))
	} else {
		xmlResp.Message = fmt.Sprintf("New diaper recorded on %s", dt.datetime.In(dt.loc).Format("Jan 2 03:04PM"))
	}

	resp, err := xml.MarshalIndent(xmlResp, " ", "  ")
	if err != nil {
		return serverError(err)
	}

	return events.APIGatewayProxyResponse{
		StatusCode: http.StatusCreated,
		Body:       string(resp),
		Headers: map[string]string{
			"content-type": "text/xml",
		},
	}, nil
}

type Datetime struct {
	datetime           time.Time
	timestamp          int64
	loc                *time.Location
	date               string
	time               string
	twentyFourHourTime bool
}

func getDatetime(message string) (*Datetime, error) {
	dt := &Datetime{}
	current := time.Now().UTC()
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return nil, err
	}
	dt.loc = loc

	dateRE := regexp.MustCompile(`.*date (?P<date>\d{0,4}(-|/){0,1}\d{1,2}(-|/)\d{1,2}).*`)
	dateMatch := dateRE.FindStringSubmatch(message)
	dateIndex := dateRE.SubexpIndex("date")
	if len(dateMatch) > 0 {
		dateValue := dateMatch[dateIndex]
		dSplit := strings.Split(dateValue, "-")
		if len(dSplit) < 2 {
			dSplit = strings.Split(dateValue, "/")
		}
		var year, month, day string
		if len(dSplit) > 2 {
			year = dSplit[0]
			month = dSplit[1]
			day = dSplit[2]
		} else {
			fmt.Println("no year match")
			year = current.In(loc).Format("2006")
			month = dSplit[0]
			day = dSplit[1]
		}
		if len(month) != 2 {
			month = fmt.Sprintf("0%s", month)
		}
		if len(day) != 2 {
			day = fmt.Sprintf("0%s", day)
		}
		dt.date = fmt.Sprintf("%s-%s-%s", year, month, day)
	}

	timeRE := regexp.MustCompile(`.*time (?P<time>\d{1,2}:\d{2})\s*(?P<meridiem>(am|pm)){0,1}.*`)
	timeMatch := timeRE.FindStringSubmatch(message)
	timeIndex := timeRE.SubexpIndex("time")
	if len(timeMatch) > 0 {
		timeValue := timeMatch[timeIndex]
		meridiemIndex := timeRE.SubexpIndex("meridiem")
		meridiemValue := timeMatch[meridiemIndex]
		if meridiemValue == "" {
			dt.twentyFourHourTime = true
			dt.time = timeValue
		} else {
			if len(timeValue) == 4 {
				timeValue = fmt.Sprintf("0%s", timeValue)
			}
			dt.time = fmt.Sprintf("%s %s", timeValue, strings.ToUpper(meridiemValue))
		}
	}

	if dt.date != "" && dt.time != "" {
		var datetime time.Time
		if dt.twentyFourHourTime {
			datetime, err = time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", dt.date, dt.time), loc)
			if err != nil {
				return nil, err
			}
		} else {
			datetime, err = time.ParseInLocation("2006-01-02 03:04 PM", fmt.Sprintf("%s %s", dt.date, dt.time), loc)
			if err != nil {
				return nil, err
			}
		}

		dt.datetime = datetime.UTC()
		dt.date = dt.datetime.Format("2006-01-02")
		dt.time = dt.datetime.Format("15:04")
	} else if dt.date != "" && dt.time == "" {
		datetime, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", dt.date, current.In(loc).Format("15:04")), loc)
		if err != nil {
			return nil, err
		}

		dt.datetime = datetime.UTC()
		dt.date = dt.datetime.Format("2006-01-02")
		dt.time = dt.datetime.Format("15:04")
	} else if dt.date == "" && dt.time != "" {
		var datetime time.Time
		if dt.twentyFourHourTime {
			datetime, err = time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", current.In(loc).Format("2006-01-02"), dt.time), loc)
			if err != nil {
				return nil, err
			}
		} else {
			datetime, err = time.ParseInLocation("2006-01-02 03:04 PM", fmt.Sprintf("%s %s", current.In(loc).Format("2006-01-02"), dt.time), loc)
			if err != nil {
				return nil, err
			}
		}

		dt.datetime = datetime.UTC()
		dt.date = dt.datetime.Format("2006-01-02")
		dt.time = dt.datetime.Format("15:04")
	} else {
		dt.datetime = current
		dt.date = dt.datetime.Format("2006-01-02")
		dt.time = dt.datetime.Format("15:04")
	}

	return dt, nil
}

func getTimestamp(message string) (*Datetime, error) {
	current := time.Now().UTC()

	dt := &Datetime{}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return nil, err
	}
	dt.loc = loc

	dateRE := regexp.MustCompile(`.*date (?P<date>\d{0,4}(-|/){0,1}\d{1,2}(-|/)\d{1,2}).*`)
	dateMatch := dateRE.FindStringSubmatch(message)
	dateIndex := dateRE.SubexpIndex("date")
	if len(dateMatch) > 0 {
		dateValue := dateMatch[dateIndex]
		dSplit := strings.Split(dateValue, "-")
		if len(dSplit) < 2 {
			dSplit = strings.Split(dateValue, "/")
		}
		var year, month, day string
		if len(dSplit) > 2 {
			year = dSplit[0]
			month = dSplit[1]
			day = dSplit[2]
		} else {
			fmt.Println("no year match")
			year = current.In(loc).Format("2006")
			month = dSplit[0]
			day = dSplit[1]
		}
		if len(month) != 2 {
			month = fmt.Sprintf("0%s", month)
		}
		if len(day) != 2 {
			day = fmt.Sprintf("0%s", day)
		}
		dt.date = fmt.Sprintf("%s-%s-%s", year, month, day)
	}

	timeRE := regexp.MustCompile(`.*time (?P<time>\d{1,2}:\d{2})\s*(?P<meridiem>(am|pm)){0,1}.*`)
	timeMatch := timeRE.FindStringSubmatch(message)
	timeIndex := timeRE.SubexpIndex("time")
	if len(timeMatch) > 0 {
		timeValue := timeMatch[timeIndex]
		meridiemIndex := timeRE.SubexpIndex("meridiem")
		meridiemValue := timeMatch[meridiemIndex]
		if meridiemValue == "" {
			dt.twentyFourHourTime = true
			dt.time = timeValue
		} else {
			if len(timeValue) == 4 {
				timeValue = fmt.Sprintf("0%s", timeValue)
			}
			dt.time = fmt.Sprintf("%s %s", timeValue, strings.ToUpper(meridiemValue))
		}
	}

	if dt.date != "" && dt.time != "" {
		var datetime time.Time
		if dt.twentyFourHourTime {
			datetime, err = time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", dt.date, dt.time), loc)
			if err != nil {
				return nil, err
			}

		} else {
			datetime, err = time.ParseInLocation("2006-01-02 03:04 PM", fmt.Sprintf("%s %s", dt.date, dt.time), loc)
			if err != nil {
				return nil, err
			}
		}
		dt.timestamp = datetime.Unix()
	} else if dt.date != "" && dt.time == "" {
		datetime, err := time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", dt.date, current.In(loc).Format("15:04")), loc)
		if err != nil {
			return nil, err
		}

		dt.timestamp = datetime.Unix()
	} else if dt.date == "" && dt.time != "" {
		var datetime time.Time
		if dt.twentyFourHourTime {
			datetime, err = time.ParseInLocation("2006-01-02 15:04", fmt.Sprintf("%s %s", current.In(loc).Format("2006-01-02"), dt.time), loc)
			if err != nil {
				return nil, err
			}
		} else {
			datetime, err = time.ParseInLocation("2006-01-02 03:04 PM", fmt.Sprintf("%s %s", current.In(loc).Format("2006-01-02"), dt.time), loc)
			if err != nil {
				return nil, err
			}
		}

		dt.timestamp = datetime.Unix()
	} else {
		dt.timestamp = current.Unix()
	}

	return dt, nil
}
