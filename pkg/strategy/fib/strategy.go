package triangle

import (
	"context"
	"fmt"
	"github.com/c9s/bbgo/pkg/bbgo"
	"github.com/c9s/bbgo/pkg/fixedpoint"
	"github.com/c9s/bbgo/pkg/types"
	"github.com/sirupsen/logrus"
)

const ID = "fib"

const One = fixedpoint.Value(1e8)

var log = logrus.WithField("strategy", ID)

func init() {
	bbgo.RegisterStrategy(ID, &Strategy{})
}

func (s *Strategy) ClosePosition(ctx context.Context, percentage fixedpoint.Value) error {
	base := s.Position.GetBase()
	if base.IsZero() {
		return fmt.Errorf("no opened %s position", s.Position.Symbol)
	}

	// make it negative
	quantity := base.Mul(percentage).Abs()
	side := types.SideTypeBuy
	if base.Sign() > 0 {
		side = types.SideTypeSell
	}

	if quantity.Compare(s.Market.MinQuantity) < 0 {
		return fmt.Errorf("order quantity %v is too small, less than %v", quantity, s.Market.MinQuantity)
	}

	submitOrder := types.SubmitOrder{
		Symbol:   s.Symbol,
		Side:     side,
		Type:     types.OrderTypeMarket,
		Quantity: quantity,
		Market:   s.Market,
	}

	//s.Notify("Submitting %s %s order to close position by %v", s.Symbol, side.String(), percentage, submitOrder)

	createdOrders, err := s.session.Exchange.SubmitOrders(ctx, submitOrder)
	if err != nil {
		//log.WithError(err).Errorf("can not place position close order")
	}

	s.orderStore.Add(createdOrders...)
	s.activeMakerOrders.Add(createdOrders...)
	return err
}

type Strategy struct {
	*bbgo.Notifiability
	Symbol   string `json:"symbol"`
	Market   types.Market
	Interval types.Interval   `json:"interval"`
	Quantity fixedpoint.Value `json:"quantity"`
	hh       fixedpoint.Value `json:"low"`
	ll       fixedpoint.Value `json:"high"`

	Position    *types.Position
	ProfitStats types.ProfitStats `json:"profitStats,omitempty"`

	submitOrders []types.SubmitOrder

	userDataStream    types.Stream
	userDataStreamCtx context.Context

	activeMakerOrders *bbgo.LocalActiveOrderBook
	orderStore        *bbgo.OrderStore
	tradeCollector    *bbgo.TradeCollector

	session *bbgo.ExchangeSession
	book    *types.StreamOrderBook
	market  types.Market
}

func (s *Strategy) ID() string {
	return ID
}

func (s *Strategy) Subscribe(session *bbgo.ExchangeSession) {
	log.Infof("subscribe %s", s.Symbol)
	session.Subscribe(types.KLineChannel, s.Symbol, types.SubscribeOptions{Interval: s.Interval.String()})
}

//func (s *Strategy) positionManager(ctx context.Context, orderExecutor bbgo.OrderExecutor, sell5 fixedpoint.Value, price0 fixedpoint.Value, buy5 fixedpoint.Value, kline *types.KLine) {
//	var submitOrders = []types.SubmitOrder{}
//
//	buyOrderTP := types.SubmitOrder{
//		Symbol:   s.Symbol,
//		Side:     types.SideTypeSell,
//		Type:     types.OrderTypeLimit,
//		Price:    price0,
//		Quantity: s.Position.GetBase().Abs().Mul(fixedpoint.NewFromFloat(0.5)),
//	}
//	buyOrderSL := types.SubmitOrder{
//		Symbol:   s.Symbol,
//		Side:     types.SideTypeSell,
//		Type:     types.OrderTypeLimit,
//		Price:    buy5.Sub(10),
//		Quantity: s.Position.GetBase().Abs(),
//	}
//	if s.Position.GetBase().Sign() > 0 && kline.Close.Compare(price0) < 0 {
//		submitOrders = append(submitOrders, buyOrderTP)
//		submitOrders = append(submitOrders, buyOrderSL)
//	}
//
//	sellOrderTP := types.SubmitOrder{
//		Symbol:   s.Symbol,
//		Side:     types.SideTypeBuy,
//		Type:     types.OrderTypeLimit,
//		Price:    price0,
//		Quantity: s.Position.GetBase().Abs().Mul(fixedpoint.NewFromFloat(0.5)),
//	}
//	sellOrderSL := types.SubmitOrder{
//		Symbol:   kline.Symbol,
//		Side:     types.SideTypeBuy,
//		Type:     types.OrderTypeLimit,
//		Price:    sell5.Add(10),
//		Quantity: s.Position.GetBase().Abs(),
//	}
//	if s.Position.GetBase().Sign() < 0 && kline.Close.Compare(price0) > 0 {
//		submitOrders = append(submitOrders, sellOrderTP)
//		submitOrders = append(submitOrders, sellOrderSL)
//	}
//
//	createdOrders, err := orderExecutor.SubmitOrders(ctx, submitOrders...)
//	if err != nil {
//		log.WithError(err).Errorf("can not place orders")
//	}
//
//	s.orderStore.Add(createdOrders...)
//	s.activeMakerOrders.Add(createdOrders...)
//}

func (s *Strategy) placeOrders(ctx context.Context, orderExecutor bbgo.OrderExecutor, session *bbgo.ExchangeSession, midPrice fixedpoint.Value, kline *types.KLine) {

	s.hh = fixedpoint.NewFromFloat(42400)    //40831.30, 40884
	s.ll = fixedpoint.NewFromFloat(40135.04) //39228.48, 40501

	if kline.High.Compare(s.hh) > 0.0 {
		s.hh = kline.High
	}
	if kline.Low.Compare(s.ll) < 0.0 {
		s.ll = kline.Low
	}

	log.Infof("ll: %v, hh: %v", s.ll, s.hh)

	t := s.hh.Sub(s.ll).Abs()
	log.Infof("fib unit length: %f", t.Float64())

	// -0.114
	sell5 := s.hh.Sub(t.Mul(fixedpoint.NewFromFloat(-0.13)))

	// 0.0
	sell4 := s.hh.Sub(t.Mul(fixedpoint.Zero))

	// 0.1
	sell3 := s.hh.Sub(t.Mul(fixedpoint.NewFromFloat(0.114)))

	// 0.236
	sell2 := s.hh.Sub(t.Mul(fixedpoint.NewFromFloat(0.236)))

	// 0.382
	sell1 := s.hh.Sub(t.Mul(fixedpoint.NewFromFloat(0.382)))

	// 0.5
	//price0 := s.hh.Sub(t.Mul(fixedpoint.NewFromFloat(0.5)))

	// 0.618
	buy1 := s.hh.Sub(t.Mul(fixedpoint.NewFromFloat(0.618)))

	// 0.786
	buy2 := s.hh.Sub(t.Mul(fixedpoint.NewFromFloat(0.786)))

	// 0.886
	buy3 := s.hh.Sub(t.Mul(fixedpoint.NewFromFloat(0.886)))

	// 1.0
	buy4 := s.hh.Sub(t.Mul(fixedpoint.One))

	// 1.114
	buy5 := s.hh.Sub(t.Mul(fixedpoint.NewFromFloat(1.236)))

	//var submitOrders []types.SubmitOrder

	//buy
	//var submitBuyOrders []types.SubmitOrder
	buyOrder1 := types.SubmitOrder{
		Symbol:   kline.Symbol,
		Side:     types.SideTypeBuy,
		Type:     types.OrderTypeLimit,
		Price:    buy1,
		Quantity: s.Quantity.Mul(fixedpoint.NewFromFloat(1)),
	}
	//submitBuyOrders = append(submitBuyOrders, buyOrder1)
	if kline.Close.Compare(buy1) > 0 {
		createdOrders, err := orderExecutor.SubmitOrders(ctx, buyOrder1)
		if err != nil {
			log.WithError(err).Errorf("can not buy place orders")
		}
		s.orderStore.Add(createdOrders...)
		s.activeMakerOrders.Add(createdOrders...)
	}
	buyOrder2 := types.SubmitOrder{
		Symbol:   kline.Symbol,
		Side:     types.SideTypeBuy,
		Type:     types.OrderTypeLimit,
		Price:    buy2,
		Quantity: s.Quantity.Mul(fixedpoint.NewFromFloat(1)),
	}
	//submitBuyOrders = append(submitBuyOrders, buyOrder2)
	if kline.Close.Compare(buy2) > 0 {
		createdOrders, err := orderExecutor.SubmitOrders(ctx, buyOrder2)
		if err != nil {
			log.WithError(err).Errorf("can not buy place orders")
		}
		s.orderStore.Add(createdOrders...)
		s.activeMakerOrders.Add(createdOrders...)
	}
	buyOrder3 := types.SubmitOrder{
		Symbol:   kline.Symbol,
		Side:     types.SideTypeBuy,
		Type:     types.OrderTypeLimit,
		Price:    buy3,
		Quantity: s.Quantity.Mul(fixedpoint.NewFromFloat(1)),
	}
	//submitBuyOrders = append(submitBuyOrders, buyOrder3)
	if kline.Close.Compare(buy3) > 0 {
		createdOrders, err := orderExecutor.SubmitOrders(ctx, buyOrder3)
		if err != nil {
			log.WithError(err).Errorf("can not buy place orders")
		}
		s.orderStore.Add(createdOrders...)
		s.activeMakerOrders.Add(createdOrders...)
	}
	buyOrder4 := types.SubmitOrder{
		Symbol:   kline.Symbol,
		Side:     types.SideTypeBuy,
		Type:     types.OrderTypeLimit,
		Price:    buy4,
		Quantity: s.Quantity.Mul(fixedpoint.NewFromFloat(1)),
	}
	//submitBuyOrders = append(submitBuyOrders, buyOrder4)
	if kline.Close.Compare(buy4) > 0 {
		createdOrders, err := orderExecutor.SubmitOrders(ctx, buyOrder4)
		if err != nil {
			log.WithError(err).Errorf("can not buy place orders")
		}
		s.orderStore.Add(createdOrders...)
		s.activeMakerOrders.Add(createdOrders...)
	}
	buyOrder5 := types.SubmitOrder{
		Symbol:   kline.Symbol,
		Side:     types.SideTypeBuy,
		Type:     types.OrderTypeLimit,
		Price:    buy5,
		Quantity: s.Quantity.Mul(fixedpoint.NewFromFloat(1)),
	}
	//submitBuyOrders = append(submitBuyOrders, buyOrder5)
	if kline.Close.Compare(buy5) > 0 {
		createdOrders, err := orderExecutor.SubmitOrders(ctx, buyOrder5)
		if err != nil {
			log.WithError(err).Errorf("can not buy place orders")
		}
		s.orderStore.Add(createdOrders...)
		s.activeMakerOrders.Add(createdOrders...)
	}

	// sell
	//var submitSellOrders []types.SubmitOrder
	sellOrder1 := types.SubmitOrder{
		Symbol:   kline.Symbol,
		Side:     types.SideTypeSell,
		Type:     types.OrderTypeLimit,
		Price:    sell1,
		Quantity: s.Quantity.Mul(fixedpoint.NewFromFloat(1)),
	}
	//submitSellOrders = append(submitSellOrders, sellOrder1)
	if kline.Close.Compare(sell1) < 0 {
		createdOrders, err := orderExecutor.SubmitOrders(ctx, sellOrder1)
		if err != nil {
			log.WithError(err).Errorf("can not sell place orders")
		}
		s.orderStore.Add(createdOrders...)
		s.activeMakerOrders.Add(createdOrders...)
	}
	sellOrder2 := types.SubmitOrder{
		Symbol:   kline.Symbol,
		Side:     types.SideTypeSell,
		Type:     types.OrderTypeLimit,
		Price:    sell2,
		Quantity: s.Quantity.Mul(fixedpoint.NewFromFloat(1)),
	}
	//submitSellOrders = append(submitSellOrders, sellOrder2)
	if kline.Close.Compare(sell2) < 0 {
		createdOrders, err := orderExecutor.SubmitOrders(ctx, sellOrder2)
		if err != nil {
			log.WithError(err).Errorf("can not sell place orders")
		}
		s.orderStore.Add(createdOrders...)
		s.activeMakerOrders.Add(createdOrders...)
	}
	sellOrder3 := types.SubmitOrder{
		Symbol:   kline.Symbol,
		Side:     types.SideTypeSell,
		Type:     types.OrderTypeLimit,
		Price:    sell3,
		Quantity: s.Quantity.Mul(fixedpoint.NewFromFloat(1)),
	}
	//submitSellOrders = append(submitSellOrders, sellOrder3)
	if kline.Close.Compare(sell3) < 0 {
		createdOrders, err := orderExecutor.SubmitOrders(ctx, sellOrder3)
		if err != nil {
			log.WithError(err).Errorf("can not sell place orders")
		}
		s.orderStore.Add(createdOrders...)
		s.activeMakerOrders.Add(createdOrders...)
	}
	sellOrder4 := types.SubmitOrder{
		Symbol:   kline.Symbol,
		Side:     types.SideTypeSell,
		Type:     types.OrderTypeLimit,
		Price:    sell4,
		Quantity: s.Quantity.Mul(fixedpoint.NewFromFloat(1)),
	}
	//submitSellOrders = append(submitSellOrders, sellOrder4)
	if kline.Close.Compare(sell4) < 0 {
		createdOrders, err := orderExecutor.SubmitOrders(ctx, sellOrder4)
		if err != nil {
			log.WithError(err).Errorf("can not sell place orders")
		}
		s.orderStore.Add(createdOrders...)
		s.activeMakerOrders.Add(createdOrders...)
	}
	sellOrder5 := types.SubmitOrder{
		Symbol:   kline.Symbol,
		Side:     types.SideTypeSell,
		Type:     types.OrderTypeLimit,
		Price:    sell5,
		Quantity: s.Quantity.Mul(fixedpoint.NewFromFloat(1)),
	}
	//submitSellOrders = append(submitSellOrders, sellOrder5)
	if kline.Close.Compare(sell5) < 0 {
		createdOrders, err := orderExecutor.SubmitOrders(ctx, sellOrder5)
		if err != nil {
			log.WithError(err).Errorf("can not sell place orders")
		}
		s.orderStore.Add(createdOrders...)
		s.activeMakerOrders.Add(createdOrders...)
	}

}

//
//func (s *Strategy) handleFilledOrder(order types.Order) {
//	log.Info(order.String())
//	log.Infof("trigger position control by filled order %d", order.OrderID)
//	if order.Side == types.SideTypeBuy {
//		log.Infof("dectect main buy order traded")
//		buyOrderTP := types.SubmitOrder{
//			Symbol:   s.Symbol,
//			Side:     types.SideTypeSell,
//			Type:     types.OrderTypeLimit,
//			Price:    order.Price.Add(fixedpoint.NewFromFloat(75)),
//			Quantity: s.Position.GetBase().Abs().Mul(fixedpoint.NewFromFloat(0.5)),
//		}
//		s.submitOrders = append(s.submitOrders, buyOrderTP)
//		buyOrderSL := types.SubmitOrder{
//			Symbol:   s.Symbol,
//			Side:     types.SideTypeSell,
//			Type:     types.OrderTypeStopMarket,
//			Price:    order.Price.Sub(5),
//			Quantity: s.Position.GetBase().Abs(),
//		}
//		s.submitOrders = append(s.submitOrders, buyOrderSL)
//	}
//	if order.Side == types.SideTypeSell {
//		log.Infof("dectect main sell order traded")
//
//		sellOrderTP := types.SubmitOrder{
//			Symbol:   s.Symbol,
//			Side:     types.SideTypeBuy,
//			Type:     types.OrderTypeLimit,
//			Price:    order.Price.Sub(fixedpoint.NewFromFloat(75)),
//			Quantity: s.Position.GetBase().Abs().Mul(fixedpoint.NewFromFloat(0.5)),
//		}
//		s.submitOrders = append(s.submitOrders, sellOrderTP)
//		sellOrderSL := types.SubmitOrder{
//			Symbol:   s.Symbol,
//			Side:     types.SideTypeBuy,
//			Type:     types.OrderTypeStopMarket,
//			Price:    order.Price.Add(5),
//			Quantity: s.Position.GetBase().Abs(),
//		}
//		s.submitOrders = append(s.submitOrders, sellOrderSL)
//	}
//	var orderExecutor bbgo.OrderExecutor
//	var ctx context.Context
//	createdOrders, err := orderExecutor.SubmitOrders(ctx, s.submitOrders...)
//	if err != nil {
//		log.WithError(err).Errorf("can not place orders")
//	}
//
//	s.orderStore.Add(createdOrders...)
//	s.activeMakerOrders.Add(createdOrders...)
//
//	//// filled event triggers the order removal from the active order store
//	//// we need to ensure we received every order update event before the execution is done.
//	//s.cancelContextIfTargetQuantityFilled()
//}

func (s *Strategy) handleTradeUpdate(ctx context.Context, orderExecutor bbgo.OrderExecutor, trade types.Trade) {
	//ignore trades that are not in the symbol we interested
	if trade.Symbol != s.Symbol {
		return
	}

	if !s.orderStore.Exists(trade.OrderID) {
		return
	}

	if !trade.Quantity.Eq(s.Quantity) {
		return
	}

	log.Infof("trigger position control by trade %d @ %f", trade.OrderID, trade.Price.Float64())

	if trade.Side == types.SideTypeBuy {
		log.Infof("dectect main buy order traded")
		buyOrderTP := types.SubmitOrder{
			Symbol:     s.Symbol,
			Side:       types.SideTypeSell,
			Type:       types.OrderTypeLimit,
			Price:      trade.Price.Add(fixedpoint.NewFromFloat(75.0)),
			Quantity:   s.Quantity.Mul(fixedpoint.NewFromFloat(0.5)),
			ReduceOnly: true,
		}
		_, err := orderExecutor.SubmitOrders(ctx, buyOrderTP)
		if err != nil {
			log.WithError(err).Errorf("can not place order BuyOrder's TakeProfit")
		}
		buyOrderSL := types.SubmitOrder{
			Symbol:     s.Symbol,
			Side:       types.SideTypeSell,
			Type:       types.OrderTypeStopLimit,
			Price:      trade.Price,
			StopPrice:  trade.Price.Sub(fixedpoint.NewFromFloat(10.0)),
			Quantity:   s.Quantity.Mul(fixedpoint.NewFromFloat(0.95)),
			ReduceOnly: true,
		}
		_, err = orderExecutor.SubmitOrders(ctx, buyOrderSL)
		if err != nil {
			log.WithError(err).Errorf("can not place order BuyOrder's StopLoss")
		}
	}
	if trade.Side == types.SideTypeSell {
		log.Infof("dectect main sell order traded")

		sellOrderTP := types.SubmitOrder{
			Symbol:     s.Symbol,
			Side:       types.SideTypeBuy,
			Type:       types.OrderTypeLimit,
			Price:      trade.Price.Sub(fixedpoint.NewFromFloat(75.0)),
			Quantity:   s.Quantity.Mul(fixedpoint.NewFromFloat(0.5)),
			ReduceOnly: true,
		}
		_, err := orderExecutor.SubmitOrders(ctx, sellOrderTP)
		if err != nil {
			log.WithError(err).Errorf("can not place order SellOrder's TakeProfit")
		}
		sellOrderSL := types.SubmitOrder{
			Symbol:     s.Symbol,
			Side:       types.SideTypeBuy,
			Type:       types.OrderTypeStopLimit,
			Price:      trade.Price,
			StopPrice:  trade.Price.Add(fixedpoint.NewFromFloat(10.0)),
			Quantity:   s.Quantity.Mul(fixedpoint.NewFromFloat(0.95)),
			ReduceOnly: true,
		}
		_, err = orderExecutor.SubmitOrders(ctx, sellOrderSL)
		if err != nil {
			log.WithError(err).Errorf("can not place order SellOrder's StopLoss")
		}
	}

	createdOrders, err := orderExecutor.SubmitOrders(ctx, s.submitOrders...)
	if err != nil {
		log.WithError(err).Errorf("can not place orders")
	}

	s.orderStore.Add(createdOrders...)
	//s.activeMakerOrders.Add(createdOrders...)

	log.Info(trade.String())
	s.tradeCollector.Process()
	s.Position.AddTrade(trade)
	log.Infof("position updated: %+v", s.Position)
}

// This strategy simply spent all available quote currency to buy the symbol whenever kline gets closed
func (s *Strategy) Run(ctx context.Context, orderExecutor bbgo.OrderExecutor, session *bbgo.ExchangeSession) error {
	// initial required information
	s.session = session

	s.userDataStream = session.Exchange.NewStream()
	//s.userDataStream.OnTradeUpdate(s.handleTradeUpdate)

	s.activeMakerOrders = bbgo.NewLocalActiveOrderBook(s.Symbol)
	//s.activeMakerOrders.OnFilled(s.handleFilledOrder)
	s.activeMakerOrders.BindStream(session.UserDataStream)

	s.orderStore = bbgo.NewOrderStore(s.Symbol)
	s.orderStore.BindStream(session.UserDataStream)

	if s.Position == nil {
		s.Position = types.NewPositionFromMarket(s.Market)
	}

	s.tradeCollector = bbgo.NewTradeCollector(s.Symbol, s.Position, s.orderStore)
	s.tradeCollector.BindStream(session.UserDataStream)

	s.tradeCollector.OnTrade(func(trade types.Trade) {
		s.Notifiability.Notify(trade)
		s.ProfitStats.AddTrade(trade)
		s.handleTradeUpdate(ctx, orderExecutor, trade)
	})

	s.tradeCollector.OnPositionUpdate(func(position *types.Position) {
		log.Infof("position changed: %s", s.Position)
		s.Notify(s.Position)
	})

	session.UserDataStream.OnStart(func() {
		log.Infof("connected")
		//if price, ok := session.LastPrice(s.Symbol); ok {
		//	s.placeOrders(ctx, orderExecutor, session, price, nil)
		//}
	})

	session.MarketDataStream.OnKLineClosed(func(kline types.KLine) {

		if kline.Symbol != s.Symbol {
			return
		}
		//SL := fixedpoint.NewFromFloat(0.01)
		//TP := fixedpoint.NewFromFloat(0.05)
		//if kline.Interval == types.Interval1m {
		//	if !s.Position.Base.IsZero() {
		//		log.Infof("current position: %f", s.Position.GetBase().Float64())
		//		// 10:600
		//		if s.Position.Base.Sign() > 0 {
		//			if kline.Close.Div(s.Position.AverageCost).Compare(fixedpoint.One.Sub(SL)) < 0 {
		//				log.Infof("long position trigger SL")
		//				s.ClosePosition(ctx, fixedpoint.NewFromFloat(1.0)) // SL
		//			}
		//			if kline.Close.Div(s.Position.AverageCost).Compare(fixedpoint.One.Add(TP)) > 0 {
		//				log.Infof("long position trigger TP")
		//				//s.ClosePosition(ctx, fixedpoint.NewFromFloat(0.2)) // TP
		//			}
		//		} else if s.Position.Base.Sign() < 0 {
		//			if kline.Close.Div(s.Position.AverageCost).Compare(fixedpoint.One.Sub(TP)) < 0 {
		//				log.Infof("short position trigger TP")
		//				//s.ClosePosition(ctx, fixedpoint.NewFromFloat(0.2)) // TP
		//			}
		//			if kline.Close.Div(s.Position.AverageCost).Compare(fixedpoint.One.Add(SL)) > 0 {
		//				log.Infof("short position trigger SL")
		//				s.ClosePosition(ctx, fixedpoint.NewFromFloat(1.0)) // SL
		//			}
		//		}
		//	}
		//}
		//s.tradeCollector.Process()

		if kline.Interval != s.Interval {
			return
		}

		if err := s.activeMakerOrders.GracefulCancel(ctx, s.session.Exchange); err != nil {
			log.WithError(err).Errorf("graceful cancel order error")
		}

		s.placeOrders(ctx, orderExecutor, session, kline.Close, &kline)

		//check if there is a canceled order had partially filled.
		s.tradeCollector.Process()
	})

	return nil
}
