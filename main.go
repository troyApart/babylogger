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
)

type Config struct {
	FeedingTableName string
	FeedingInterval  time.Duration
	DiaperTableName  string
	UserTableName    string
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
		"diaper_table":     c.DiaperTableName,
		"user_table":       c.UserTableName}).
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
	userid   int64
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
		err := b.userLookup(req.QueryStringParameters["From"])
		if err != nil {
			break
		}

		urlUnescape, err := url.QueryUnescape(req.QueryStringParameters["Body"])
		if err != nil {
			break
		}
		commands := strings.Split(urlUnescape, "\n")

		type returns struct {
			respMessage string
			err         error
		}
		var r []returns
		for _, command := range commands {
			var respMessage string
			var err error
			message := strings.ToLower(command)
			if strings.HasPrefix(message, LatestFeedRequest) {
				respMessage, err = b.NextFeed(message)
			} else if strings.HasPrefix(message, NewFeedRequest) {
				respMessage, err = b.NewFeed(message)
			} else if strings.HasPrefix(message, UpdateFeedRequest) {
				respMessage, err = b.UpdateFeed(message)
			} else if strings.HasPrefix(message, NewDiaperRequest) {
				respMessage, err = b.NewDiaper(message)
			} else if strings.HasPrefix(message, ListFeeds) {
				respMessage, err = b.ListFeeds(message)
			} else if strings.HasPrefix(message, ListDiapers) {
				respMessage, err = b.ListDiapers(message)
			}
			r = append(r, returns{respMessage: respMessage, err: err})
		}

		var errs int
		for _, ret := range r {
			if ret.respMessage != "" {
				if xmlResp.Message == "" {
					xmlResp.Message = string(ret.respMessage)
				} else {
					xmlResp.Message = fmt.Sprintf("%s\n%s", xmlResp.Message, string(ret.respMessage))
				}
			}

			if ret.err != nil {
				errs++
				log.Error(ret.err.Error())
				errMessage := "An error occurred"
				if xmlResp.Message == "" {
					xmlResp.Message = errMessage
				} else {
					xmlResp.Message = fmt.Sprintf("%s\n%s", xmlResp.Message, errMessage)
				}
			}
		}
		if errs == len(r) {
			break
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
		return events.APIGatewayProxyResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       strconv.Itoa(http.StatusInternalServerError),
			Headers: map[string]string{
				"content-type": "text/xml",
			},
		}, err
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

type UserRecord struct {
	UserID int64 `json:"id"`
}

func (b *BabyLogger) userLookup(phoneNumber string) error {
	builder := expression.NewBuilder()
	key := expression.Key("number").Equal(expression.Value(phoneNumber))
	proj := expression.NamesList(expression.Name("id"))
	expr, err := builder.WithKeyCondition(key).WithProjection(proj).Build()
	if err != nil {
		return err
	}

	qi := &dynamodb.QueryInput{
		TableName:                 aws.String(b.config.UserTableName),
		Limit:                     aws.Int64(1),
		ScanIndexForward:          aws.Bool(false),
		ExpressionAttributeNames:  expr.Names(),
		ExpressionAttributeValues: expr.Values(),
		ProjectionExpression:      expr.Projection(),
		KeyConditionExpression:    expr.KeyCondition(),
	}

	o, err := b.dynamodb.Query(qi)
	if err != nil {
		return err
	}
	log.WithField("output", o.String()).Info("dynamodb query succeeded")

	if len(o.Items) != 1 {
		return err
	}
	var ur UserRecord
	err = dynamodbattribute.UnmarshalMap(o.Items[0], &ur)
	if err != nil {
		return err
	}

	b.userid = ur.UserID
	return nil
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

func getLast(tableName string, db dynamodber, userid int64, skipBottle bool) (*FeedingRecord, error) {
	var lastTimestamp int64
	var keyCond *expression.KeyConditionBuilder
	for i := 1; i < 5; i++ {
		builder := expression.NewBuilder()
		key := expression.Key("userid").Equal(expression.Value(userid))
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
func (b *BabyLogger) NextFeed(message string) (string, error) {
	fr, err := getLast(b.config.FeedingTableName, b.dynamodb, b.userid, true)
	if err != nil {
		return "", err
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
		count, err = strconv.ParseInt(countString, 10, 64)
		if err != nil {
			count = 1
		}
	}

	previousTime := time.Unix(fr.Timestamp, 0)
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return "", err
	}

	var respMessage string
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
		respMessage = "The next feedings are:"
		for i := int64(0); i < count; i++ {
			respMessage = fmt.Sprintf("%s\n%s: %s", respMessage, nextSide, nextTime.Format("Jan 2 03:04PM"))
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
		respMessage = fmt.Sprintf("The next feeding is on your %s side on %s", nextSide, nextTime.Format("Jan 2 03:04PM"))
	}

	return respMessage, nil
}

func getAllForDay(tableName string, db dynamodber, userid int64, dt *Datetime, proj expression.ProjectionBuilder, output interface{}) error {
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

	key := expression.KeyEqual(expression.Key("userid"), expression.Value(userid))
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

func (b *BabyLogger) ListFeeds(message string) (string, error) {
	dt, err := getTimestamp(message)
	if err != nil {
		return "", err
	}

	proj := expression.NamesList(
		expression.Name("timestamp"),
		expression.Name("side"),
		expression.Name("left"),
		expression.Name("right"),
		expression.Name("bottle"))
	ffr := make([]FullFeedingRecord, 0)
	err = getAllForDay(b.config.FeedingTableName, b.dynamodb, b.userid, dt, proj, &ffr)
	if err != nil {
		return "", err
	}

	respMessage := fmt.Sprintf("Feedings on %s", time.Unix(dt.timestamp, 0).In(dt.loc).Format("2006-01-02"))
	var left, right, count int64
	var bottle float64
	for _, record := range ffr {
		timeInLoc := time.Unix(record.Timestamp, 0).In(dt.loc).Format("15:04")
		respMessage = fmt.Sprintf("%s\n%s -", respMessage, timeInLoc)
		count++
		if record.Side == BottleSide {
			respMessage = fmt.Sprintf("%s %s %.2foz", respMessage, record.Side, record.Bottle)
			bottle += record.Bottle
		} else {
			respMessage = fmt.Sprintf("%s %s L: %dmin R: %dmin", respMessage, record.Side, record.Left, record.Right)
			left += record.Left
			right += record.Right
		}
	}
	respMessage = fmt.Sprintf("%s\nTotal: %d, Left: %dmin, Right: %dmin, Bottle: %.2foz", respMessage, count, left, right, bottle)

	return respMessage, nil
}

func (b *BabyLogger) ListDiapers(message string) (string, error) {
	dt, err := getTimestamp(message)
	if err != nil {
		return "", err
	}

	proj := expression.NamesList(expression.Name("timestamp"), expression.Name("wet"), expression.Name("soiled"))
	fdr := make([]FullDiaperRecord, 0)
	err = getAllForDay(b.config.DiaperTableName, b.dynamodb, b.userid, dt, proj, &fdr)
	if err != nil {
		return "", err
	}

	respMessage := fmt.Sprintf("Diapers on %s", time.Unix(dt.timestamp, 0).In(dt.loc).Format("2006-01-02"))
	var total, wet, soiled int
	for _, record := range fdr {
		timeInLoc := time.Unix(record.Timestamp, 0).In(dt.loc).Format("15:04")
		respMessage = fmt.Sprintf("%s\n%s - ", respMessage, timeInLoc)
		if record.Wet {
			respMessage = fmt.Sprintf("%s %s", respMessage, "Wet")
			wet++
		}
		if record.Soiled {
			respMessage = fmt.Sprintf("%s %s", respMessage, "Soiled")
			soiled++
		}
		total++
	}
	respMessage = fmt.Sprintf("%s\nTotal: %d, Wet: %d, Soiled: %d", respMessage, total, wet, soiled)

	return respMessage, nil
}

func (b *BabyLogger) NewFeed(message string) (string, error) {
	re := regexp.MustCompile(`^feed (?P<side>[A-Za-z]+).*`)
	match := re.FindStringSubmatch(message)
	index := re.SubexpIndex("side")
	side := match[index]
	if side != LeftSide && side != RightSide && side != BottleSide {
		return "", fmt.Errorf("Invalid side specified")
	}

	dt, err := getTimestamp(message)
	if err != nil {
		return "", nil
	}

	leftRE := regexp.MustCompile(`.*left (?P<left>\d+)(\s+|$).*`)
	leftMatch := leftRE.FindStringSubmatch(message)
	leftIndex := leftRE.SubexpIndex("left")
	var left string
	if len(leftMatch) > 0 {
		left = leftMatch[leftIndex]
	}

	rightRE := regexp.MustCompile(`.*right (?P<right>\d+)(\s+|$).*`)
	rightMatch := rightRE.FindStringSubmatch(message)
	rightIndex := rightRE.SubexpIndex("right")
	var right string
	if len(rightMatch) > 0 {
		right = rightMatch[rightIndex]
	}

	bottleRE := regexp.MustCompile(`.*bottle (?P<bottle>\d*\.{0,1}\d{0,2})(\s+|$).*`)
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

				N: aws.String(strconv.FormatInt(b.userid, 10)),
			},
			"timestamp": {
				N: aws.String(strconv.FormatInt(dt.timestamp, 10)),
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
		return "", nil
	}
	log.WithField("output", o).Info("dynamodb put succeeded")

	var sideString string
	if side == BottleSide {
		sideString = fmt.Sprintf("using %s", side)
	} else {
		sideString = fmt.Sprintf("starting on %s side", side)
	}
	var respMessage string
	if dt.twentyFourHourTime {
		respMessage = fmt.Sprintf("New feeding recorded on %s %s", time.Unix(dt.timestamp, 0).In(dt.loc).Format("Jan 2 15:04"), sideString)
	} else {
		respMessage = fmt.Sprintf("New feeding recorded on %s %s", time.Unix(dt.timestamp, 0).In(dt.loc).Format("Jan 2 03:04PM"), sideString)
	}

	return respMessage, nil
}

func (b *BabyLogger) UpdateFeed(message string) (string, error) {
	re := regexp.MustCompile(`^update last.*`)
	match := re.FindStringSubmatch(message)
	var dt *Datetime
	if len(match) != 0 {
		fr, err := getLast(b.config.FeedingTableName, b.dynamodb, b.userid, false)
		if err != nil {
			return "", err
		}

		dt = &Datetime{}
		dt.timestamp = fr.Timestamp
		loc, err := time.LoadLocation("America/Los_Angeles")
		if err != nil {
			return "", err
		}
		dt.loc = loc
	} else {
		var err error
		dt, err = getTimestamp(message)
		if err != nil {
			return "", err
		}
	}

	cond := expression.Name("timestamp").Equal(expression.Value(dt.timestamp))
	builder := expression.NewBuilder().WithCondition(cond)
	var update expression.UpdateBuilder

	leftRE := regexp.MustCompile(`.*left (?P<set>(set|add|sub)){0,1}\s*(?P<left>\d+)(\s+|$).*`)
	leftMatch := leftRE.FindStringSubmatch(message)
	leftSetIndex := leftRE.SubexpIndex("set")
	leftIndex := leftRE.SubexpIndex("left")
	var left string
	if len(leftMatch) > 0 {
		left = leftMatch[leftIndex]
		leftValue, err := strconv.ParseInt(left, 10, 64)
		if err != nil {
			return "", err
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

	rightRE := regexp.MustCompile(`.*right (?P<set>(set|add|sub)){0,1}\s*(?P<right>\d+)(\s+|$).*`)
	rightMatch := rightRE.FindStringSubmatch(message)
	rightSetIndex := rightRE.SubexpIndex("set")
	rightIndex := rightRE.SubexpIndex("right")
	var right string
	if len(rightMatch) > 0 {
		right = rightMatch[rightIndex]
		rightValue, err := strconv.ParseInt(right, 10, 64)
		if err != nil {
			return "", err
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

	bottleRE := regexp.MustCompile(`.*bottle (?P<set>(set|add|sub)){0,1}\s*(?P<bottle>\d*\.{0,1}\d{0,2})(\s+|$).*`)
	bottleMatch := bottleRE.FindStringSubmatch(message)
	bottleSetIndex := bottleRE.SubexpIndex("set")
	bottleIndex := bottleRE.SubexpIndex("bottle")
	var bottle string
	if len(bottleMatch) > 0 {
		bottle = bottleMatch[bottleIndex]
		bottleValue, err := strconv.ParseFloat(bottle, 64)
		if err != nil {
			return "", err
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
		return "", fmt.Errorf("Nothing to update")
	}

	builder.WithUpdate(update)
	expr, err := builder.Build()
	if err != nil {
		return "", err
	}

	i := &dynamodb.UpdateItemInput{
		TableName: aws.String(b.config.FeedingTableName),
		Key: map[string]*dynamodb.AttributeValue{
			"userid": {
				N: aws.String(strconv.FormatInt(b.userid, 10)),
			},
			"timestamp": {
				N: aws.String(strconv.FormatInt(dt.timestamp, 10)),
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
		return "", err
	}
	log.WithField("output", o).Info("dynamodb put succeeded")

	var respMessage string
	if dt.twentyFourHourTime {
		respMessage = fmt.Sprintf("Updated feeding recorded on %s", time.Unix(dt.timestamp, 0).In(dt.loc).Format("Jan 2 15:04"))
	} else {
		respMessage = fmt.Sprintf("Updated feeding recorded on %s", time.Unix(dt.timestamp, 0).In(dt.loc).Format("Jan 2 03:04PM"))
	}

	return respMessage, nil
}

func (b *BabyLogger) NewDiaper(message string) (string, error) {
	dt, err := getTimestamp(message)
	if err != nil {
		return "", err
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
				N: aws.String(strconv.FormatInt(b.userid, 10)),
			},
			"timestamp": {
				N: aws.String(strconv.FormatInt(dt.timestamp, 10)),
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
		return "", err
	}
	log.WithField("output", o).Info("dynamodb put succeeded")

	var respMessage string
	if dt.twentyFourHourTime {
		respMessage = fmt.Sprintf("New diaper recorded on %s", time.Unix(dt.timestamp, 0).In(dt.loc).Format("Jan 2 15:04"))
	} else {
		respMessage = fmt.Sprintf("New diaper recorded on %s", time.Unix(dt.timestamp, 0).In(dt.loc).Format("Jan 2 03:04PM"))
	}

	return respMessage, nil
}

type Datetime struct {
	datetime           time.Time
	timestamp          int64
	loc                *time.Location
	date               string
	time               string
	twentyFourHourTime bool
}

func getTimestamp(message string) (*Datetime, error) {
	current := time.Now().UTC()

	dt := &Datetime{}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		return nil, err
	}
	dt.loc = loc

	dateRE := regexp.MustCompile(`.*\s+(?P<year>\d{4})*(-|/)*(?P<month>\d{1,2})(-|/)(?P<day>\d{1,2}).*`)
	dateMatch := dateRE.FindStringSubmatch(message)
	if len(dateMatch) > 0 {
		year := dateMatch[dateRE.SubexpIndex("year")]
		month := dateMatch[dateRE.SubexpIndex("month")]
		day := dateMatch[dateRE.SubexpIndex("day")]
		if year == "" {
			year = current.In(loc).Format("2006")
		}
		if len(month) != 2 {
			month = fmt.Sprintf("0%s", month)
		}
		if len(day) != 2 {
			day = fmt.Sprintf("0%s", day)
		}
		dt.date = fmt.Sprintf("%s-%s-%s", year, month, day)
	}

	timeRE := regexp.MustCompile(`.*\s+(?P<time>\d{1,2}:\d{2})\s*(?P<meridiem>(am|pm))*.*`)
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
