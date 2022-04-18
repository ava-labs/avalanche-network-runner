use std::{
    io::{self, Error, ErrorKind},
    sync::{Arc, Mutex},
};

use log::info;
use tonic::transport::Channel;

pub mod rpcpb {
    tonic::include_proto!("rpcpb");
}
use rpcpb::{
    control_service_client::ControlServiceClient, ping_service_client::PingServiceClient,
    HealthRequest, HealthResponse, PingRequest, PingResponse, StartRequest, StartResponse,
    StatusRequest, StatusResponse, StopRequest, StopResponse, UrIsRequest,
};

pub struct Client<T> {
    pub rpc_endpoint: String,
    /// Shared gRPC client connections.
    pub grpc_client: Arc<GrpcClient<T>>,
}

pub struct GrpcClient<T> {
    pub ping_client: Mutex<PingServiceClient<T>>,
    pub control_client: Mutex<ControlServiceClient<T>>,
}

impl Client<Channel> {
    /// Creates a new network-runner client.
    ///
    /// # Arguments
    ///
    /// * `rpc_endpoint` - HTTP RPC endpoint to the network runner server.
    pub async fn new(rpc_endpoint: &str) -> Self {
        info!("creating a new client with {}", rpc_endpoint);
        let ep = String::from(rpc_endpoint);
        let ping_client = PingServiceClient::connect(ep.clone()).await.unwrap();
        let control_client = ControlServiceClient::connect(ep).await.unwrap();
        let grpc_client = GrpcClient {
            ping_client: Mutex::new(ping_client),
            control_client: Mutex::new(control_client),
        };
        Self {
            rpc_endpoint: String::from(rpc_endpoint),
            grpc_client: Arc::new(grpc_client),
        }
    }

    /// Pings the network-runner server.
    pub async fn ping(&self) -> io::Result<PingResponse> {
        let mut ping_client = self.grpc_client.ping_client.lock().unwrap();
        let req = tonic::Request::new(PingRequest {});
        let resp = ping_client
            .ping(req)
            .await
            .map_err(|e| Error::new(ErrorKind::Other, format!("failed ping '{}'", e)))?;

        let ping_resp = resp.into_inner();
        Ok(ping_resp)
    }

    /// Starts a cluster.
    pub async fn start(&self, req: StartRequest) -> io::Result<StartResponse> {
        let mut control_client = self.grpc_client.control_client.lock().unwrap();
        let req = tonic::Request::new(req);
        let resp = control_client
            .start(req)
            .await
            .map_err(|e| Error::new(ErrorKind::Other, format!("failed stop '{}'", e)))?;

        let start_resp = resp.into_inner();
        Ok(start_resp)
    }

    /// Fetches the current cluster health information via network-runner.
    pub async fn health(&self) -> io::Result<HealthResponse> {
        let mut control_client = self.grpc_client.control_client.lock().unwrap();
        let req = tonic::Request::new(HealthRequest {});
        let resp = control_client
            .health(req)
            .await
            .map_err(|e| Error::new(ErrorKind::Other, format!("failed status '{}'", e)))?;

        let health_resp = resp.into_inner();
        Ok(health_resp)
    }

    /// Fetches the URIs for the current cluster.
    pub async fn uris(&self) -> io::Result<Vec<String>> {
        let mut control_client = self.grpc_client.control_client.lock().unwrap();
        let req = tonic::Request::new(UrIsRequest {});
        let resp = control_client
            .ur_is(req)
            .await
            .map_err(|e| Error::new(ErrorKind::Other, format!("failed status '{}'", e)))?;

        let uris_resp = resp.into_inner();
        Ok(uris_resp.uris)
    }

    /// Fetches the current cluster status via network-runner.
    pub async fn status(&self) -> io::Result<StatusResponse> {
        let mut control_client = self.grpc_client.control_client.lock().unwrap();
        let req = tonic::Request::new(StatusRequest {});
        let resp = control_client
            .status(req)
            .await
            .map_err(|e| Error::new(ErrorKind::Other, format!("failed status '{}'", e)))?;

        let status_resp = resp.into_inner();
        Ok(status_resp)
    }

    /// Stop the currently running cluster.
    pub async fn stop(&self) -> io::Result<StopResponse> {
        let mut control_client = self.grpc_client.control_client.lock().unwrap();
        let req = tonic::Request::new(StopRequest {});
        let resp = control_client
            .stop(req)
            .await
            .map_err(|e| Error::new(ErrorKind::Other, format!("failed stop '{}'", e)))?;

        let stop_resp = resp.into_inner();
        Ok(stop_resp)
    }
}
